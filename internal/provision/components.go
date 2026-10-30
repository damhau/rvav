package provision

import (
	"strings"

	"go.temporal.io/sdk/workflow"
)

// Multi-VM blueprints (ADR-0024): every VM is built at the same time, in
// phases, under its component's step.

// eachVM runs fn for every VM at the same time and waits for all of them:
// one failing doesn't stop the others halfway (a clone stopped there would
// leave a VM behind). It returns the step key and error of the first to
// fail.
func eachVM(ctx workflow.Context, n int, fn func(ctx workflow.Context, i int) (string, error)) (string, error) {
	keys, errs := make([]string, n), make([]error, n)
	finished := workflow.NewBufferedChannel(ctx, n)
	for i := 0; i < n; i++ {
		workflow.Go(ctx, func(gctx workflow.Context) {
			keys[i], errs[i] = fn(gctx, i)
			finished.Send(gctx, i)
		})
	}
	first := -1
	for range n {
		var i int
		finished.Receive(ctx, &i)
		if errs[i] != nil && first < 0 {
			first = i
		}
	}
	if first < 0 {
		return "", nil
	}
	return keys[first], errs[first]
}

// runComponents builds a multi-VM blueprint's plan.
func runComponents(ctx workflow.Context, info Info) (string, error) {
	_, failed, err := buildComponents(ctx, info, info.Plan.VMs, info.Plan.Components, planKeys())
	return failed, err
}

// buildComponents builds components' VMs: their addresses, records and
// clones; the DRS rules keeping VMs apart; their customization and power
// on; then each component's post-configuration, in order. Steps are named
// by keys.
func buildComponents(ctx workflow.Context, info Info, vms []VMUnit, units []ComponentUnit, keys vmKeys) ([]builtVM, string, error) {
	var a *Activities
	p := Plan{VMs: vms, Components: units, Context: info.Plan.Context}
	comps := map[string]ComponentUnit{}
	for _, c := range p.Components {
		comps[c.Key] = c
	}
	input := func(vm VMUnit) VMInput {
		c := comps[vm.Component]
		in := VMInput{RequestID: info.RequestID, ProjectID: info.ProjectID, VM: vm}
		in.Guest = c.Guest
		if c.AntiAffinity {
			in.HostIndex = vm.Index
		}
		return in
	}
	built := make([]builtVM, len(p.VMs))
	fresh := make([]bool, len(p.VMs)) // built again in this run

	// A component's step runs until its post-configuration is done; when
	// one fails, the others that hadn't finished are cancelled, so a retry
	// takes them up again.
	var started []ComponentUnit
	for _, c := range p.Components {
		if keys.parent(c) != "" && len(p.ComponentVMs(c.Key)) > 0 && !info.done(keys.parent(c)) {
			started = append(started, c)
		}
	}
	finished := map[string]bool{}
	mark := func(c ComponentUnit, status, why string) {
		_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, keys.parent(c), status, why).Get(ctx, nil)
	}
	for _, c := range started {
		mark(c, "running", "")
	}
	fail := func(key string, err error) ([]builtVM, string, error) {
		for _, c := range started {
			switch {
			case finished[c.Key]:
			case strings.HasPrefix(key, keys.parent(c)+"/"):
				mark(c, "failed", errMessage(err))
			default:
				mark(c, "cancelled", "")
			}
		}
		return nil, key, err
	}

	// 1. Addresses, DNS records and clones. Steps done before a retry are
	// skipped with what they built; what is gone since is built again, and
	// what depends on it.
	failed, err := eachVM(ctx, len(p.VMs), func(ctx workflow.Context, i int) (string, error) {
		vm := p.VMs[i]
		c, in := comps[vm.Component], input(vm)
		before := info.BuiltVMs[vm.Name]
		in.Key = keys.vm(c, KeyIPAM, vm)
		newIP := !info.done(in.Key) || before.IP == ""
		in.IP = before.IP
		if newIP {
			if err := workflow.ExecuteActivity(ctx, a.ReserveIP, in).Get(ctx, &in.IP); err != nil {
				return in.Key, err
			}
		}
		newVM := !info.done(keys.vm(c, KeyClone, vm)) || before.Moref == ""
		in.Key = keys.vm(c, KeyDNS, vm)
		if !info.done(in.Key) || newIP || newVM {
			if err := workflow.ExecuteActivity(ctx, a.CreateDNS, in).Get(ctx, nil); err != nil {
				return in.Key, err
			}
		}
		in.Key = keys.vm(c, KeyClone, vm)
		in.Moref = before.Moref
		if newVM {
			if err := workflow.ExecuteActivity(ctx, a.CloneVM, in).Get(ctx, &in.Moref); err != nil {
				return in.Key, err
			}
		}
		built[i], fresh[i] = builtVM{vm: vm, ip: in.IP, moref: in.Moref}, newVM
		return "", nil
	})
	if err != nil {
		return fail(failed, err)
	}

	// 2. The DRS rules, again when one of their VMs was built again.
	for _, c := range p.Components {
		if !c.AntiAffinity {
			continue
		}
		in := RuleInput{RequestID: info.RequestID, ProjectID: info.ProjectID, Key: keys.rule(c), Component: c.Key}
		again := false
		for i, b := range built {
			if b.vm.Component == c.Key {
				in.DeploymentID, in.Cluster, in.VCenter = b.vm.DeploymentID, b.vm.Cluster, b.vm.VCenter
				in.VMs, in.Morefs = append(in.VMs, b.vm.Name), append(in.Morefs, b.moref)
				again = again || fresh[i]
			}
		}
		in.Name = c.RuleName(in.DeploymentID)
		// Done, unchanged and still there (a rollback that stopped halfway
		// may have removed it).
		if len(in.VMs) < 2 || info.done(in.Key) && !again && info.Rules[in.Name] {
			continue
		}
		if err := workflow.ExecuteActivity(ctx, a.CreateDRSRule, in).Get(ctx, nil); err != nil {
			return fail(in.Key, err)
		}
	}

	// 3. Customization and power on.
	failed, err = eachVM(ctx, len(p.VMs), func(ctx workflow.Context, i int) (string, error) {
		vm := p.VMs[i]
		c, in := comps[vm.Component], input(vm)
		in.IP, in.Moref = built[i].ip, built[i].moref
		for _, step := range []struct {
			key string
			fn  any
		}{{KeyCustomize, a.Customize}, {KeyPowerOn, a.PowerOn}} {
			in.Key = keys.vm(c, step.key, vm)
			if info.done(in.Key) && !fresh[i] {
				continue
			}
			if err := workflow.ExecuteActivity(ctx, step.fn, in).Get(ctx, nil); err != nil {
				return in.Key, err
			}
		}
		return joinGroups(ctx, info, in, keys.vm(c, KeyNSXTags, vm), fresh[i])
	})
	if err != nil {
		return fail(failed, err)
	}

	// 4. Post-configuration, a component at a time, on its own VMs.
	for _, c := range p.Components {
		var vms []builtVM
		for _, b := range built {
			if b.vm.Component == c.Key {
				vms = append(vms, b)
			}
		}
		if len(vms) == 0 {
			continue
		}
		list := make([]VMUnit, len(vms))
		for i, b := range vms {
			list[i] = b.vm
		}
		if failed, err := postConfig(ctx, info, vms, c.PostConfig, keys.inventory(c),
			func(j Job) string { return keys.job(c, j) }, names(list), p.Context); err != nil {
			return fail(failed, err)
		}
		if keys.parent(c) != "" && !info.done(keys.parent(c)) {
			mark(c, "done", "")
		}
		finished[c.Key] = true
	}
	return built, "", nil
}
