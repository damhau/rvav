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

// runComponents builds a multi-VM blueprint's VMs: their addresses, records
// and clones; the DRS rules keeping VMs apart; their customization and
// power on; then each component's post-configuration, in order.
func runComponents(ctx workflow.Context, info Info) (string, error) {
	var a *Activities
	p := info.Plan
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
		if len(p.ComponentVMs(c.Key)) > 0 && !info.done(c.StepKey()) {
			started = append(started, c)
		}
	}
	finished := map[string]bool{}
	mark := func(c ComponentUnit, status, why string) {
		_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, c.StepKey(), status, why).Get(ctx, nil)
	}
	for _, c := range started {
		mark(c, "running", "")
	}
	fail := func(key string, err error) (string, error) {
		for _, c := range started {
			switch {
			case finished[c.Key]:
			case strings.HasPrefix(key, c.StepKey()+"/"):
				mark(c, "failed", errMessage(err))
			default:
				mark(c, "cancelled", "")
			}
		}
		return key, err
	}

	// 1. Addresses, DNS records and clones. Steps done before a retry are
	// skipped with what they built; what is gone since is built again, and
	// what depends on it.
	failed, err := eachVM(ctx, len(p.VMs), func(ctx workflow.Context, i int) (string, error) {
		vm := p.VMs[i]
		c, in := comps[vm.Component], input(vm)
		before := info.BuiltVMs[vm.Name]
		in.Key = c.VMKey(KeyIPAM, vm)
		newIP := !info.done(in.Key) || before.IP == ""
		in.IP = before.IP
		if newIP {
			if err := workflow.ExecuteActivity(ctx, a.ReserveIP, in).Get(ctx, &in.IP); err != nil {
				return in.Key, err
			}
		}
		newVM := !info.done(c.VMKey(KeyClone, vm)) || before.Moref == ""
		in.Key = c.VMKey(KeyDNS, vm)
		if !info.done(in.Key) || newIP || newVM {
			if err := workflow.ExecuteActivity(ctx, a.CreateDNS, in).Get(ctx, nil); err != nil {
				return in.Key, err
			}
		}
		in.Key = c.VMKey(KeyClone, vm)
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
		in := RuleInput{RequestID: info.RequestID, ProjectID: info.ProjectID, Key: c.StepKey() + "/" + KeyDRSRule, Component: c.Key}
		again := false
		for i, b := range built {
			if b.vm.Component == c.Key {
				in.DeploymentID, in.Cluster = b.vm.DeploymentID, b.vm.Cluster
				in.VMs, in.Morefs = append(in.VMs, b.vm.Name), append(in.Morefs, b.moref)
				again = again || fresh[i]
			}
		}
		if len(in.VMs) < 2 || info.done(in.Key) && !again {
			continue
		}
		in.Name = c.RuleName(in.DeploymentID)
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
			in.Key = c.VMKey(step.key, vm)
			if info.done(in.Key) && !fresh[i] {
				continue
			}
			if err := workflow.ExecuteActivity(ctx, step.fn, in).Get(ctx, nil); err != nil {
				return in.Key, err
			}
		}
		return "", nil
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
		parent := c.StepKey()
		if failed, err := postConfig(ctx, info, vms, c.PostConfig, parent+"/"+KeyInventory,
			func(j Job) string { return parent + "/" + KeyJob + j.Key }, names(list), p.Context); err != nil {
			return fail(failed, err)
		}
		if !info.done(parent) {
			mark(c, "done", "")
		}
		finished[c.Key] = true
	}
	return "", nil
}
