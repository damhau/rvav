package provision

import (
	"slices"
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
	_, failed, err := buildComponents(ctx, info, info.Plan.VMs, info.Plan.Components, planKeys(), nil)
	return failed, err
}

// buildComponents builds components' VMs in rounds: a component in the
// round after those it depends on, the components of a round at the same
// time (ADR-0024). Without dependencies there is one round. kept are the
// deployment's VMs an update keeps (ADR-0042): the new VMs see them as
// built before.
func buildComponents(ctx workflow.Context, info Info, vms []VMUnit, units []ComponentUnit, keys vmKeys, kept []builtVM) ([]builtVM, string, error) {
	rounds := buildRounds(units)
	if len(rounds) <= 1 {
		return buildRound(ctx, info, vms, units, keys, kept)
	}
	var all []builtVM
	for _, round := range rounds {
		var list []VMUnit
		for _, vm := range vms {
			if slices.ContainsFunc(round, func(c ComponentUnit) bool { return c.Key == vm.Component }) {
				list = append(list, vm)
			}
		}
		built, failed, err := buildRound(ctx, info, list, round, keys, append(slices.Clone(kept), all...))
		if err != nil {
			return nil, failed, err
		}
		all = append(all, built...)
	}
	// In the plan's order, as one round gives them.
	out := make([]builtVM, 0, len(all))
	for _, vm := range vms {
		if i := slices.IndexFunc(all, func(b builtVM) bool { return b.vm.Name == vm.Name }); i >= 0 {
			out = append(out, all[i])
		}
	}
	return out, "", nil
}

// buildRounds groups components by how many others are built before them.
func buildRounds(units []ComponentUnit) [][]ComponentUnit {
	planned := map[string]bool{}
	for _, c := range units {
		planned[c.Key] = true
	}
	round := map[string]int{}
	// A cycle is refused when the blueprint is saved; bounded all the same.
	for range units {
		for _, c := range units {
			r := 0
			for _, d := range c.DependsOn {
				if planned[d] && d != c.Key {
					r = max(r, round[d]+1)
				}
			}
			round[c.Key] = min(r, len(units)-1)
		}
	}
	var out [][]ComponentUnit
	for r := range len(units) {
		var list []ComponentUnit
		for _, c := range units {
			if round[c.Key] == r {
				list = append(list, c)
			}
		}
		if len(list) > 0 {
			out = append(out, list)
		}
	}
	return out
}

// componentOutputs is what the components named built, as tags and
// cloud-init read them: components.<key>.count, names, fqdns, addresses,
// and vms, each with its name, fqdn, address and index. A component with
// nothing built has a count of 0.
func componentOutputs(vms []builtVM, keys []string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		var list []builtVM
		for _, b := range vms {
			if b.vm.Component == k {
				list = append(list, b)
			}
		}
		slices.SortFunc(list, func(a, b builtVM) int { return a.vm.Index - b.vm.Index })
		names, fqdns, addresses, each := []any{}, []any{}, []any{}, []any{}
		for _, b := range list {
			names, fqdns, addresses = append(names, b.vm.Name), append(fqdns, b.vm.FQDN), append(addresses, b.ip)
			each = append(each, map[string]any{"name": b.vm.Name, "fqdn": b.vm.FQDN, "address": b.ip, "index": b.vm.Index})
		}
		out[k] = map[string]any{"count": len(list), "names": names, "fqdns": fqdns, "addresses": addresses, "vms": each}
	}
	return out
}

// buildRound builds components' VMs: their addresses, records and clones;
// the DRS rules keeping VMs apart; their customization and power on; then
// each component's post-configuration, in order. earlier are the VMs built
// in earlier rounds. Steps are named by keys.
func buildRound(ctx workflow.Context, info Info, vms []VMUnit, units []ComponentUnit, keys vmKeys, earlier []builtVM) ([]builtVM, string, error) {
	var a *Activities
	p := Plan{VMs: vms, Components: units, Context: info.Plan.Context}
	comps := map[string]ComponentUnit{}
	for _, c := range p.Components {
		comps[c.Key] = c
	}
	input := func(vm VMUnit, known []builtVM) VMInput {
		c := comps[vm.Component]
		in := VMInput{RequestID: info.RequestID, ProjectID: info.ProjectID, VM: vm}
		in.Guest = c.Guest
		if c.AntiAffinity {
			in.HostIndex = vm.Index
		}
		if len(c.Reads) > 0 {
			in.Components = componentOutputs(known, c.Reads)
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
		c, in := comps[vm.Component], input(vm, earlier)
		before := info.BuiltVMs[vm.Name]
		in.Key = keys.vm(c, KeyIPAM, vm)
		newIP := vm.Reserves() && (!info.done(in.Key) || before.IP == "" && !vm.DHCP)
		in.IP = before.IP
		if newIP {
			if err := workflow.ExecuteActivity(ctx, a.ReserveIP, in).Get(ctx, &in.IP); err != nil {
				return in.Key, err
			}
		}
		if vm.DHCP {
			in.IP = before.GuestIP
		}
		newVM := !info.done(keys.vm(c, KeyClone, vm)) || before.Moref == ""
		in.Key = keys.vm(c, KeyDNS, vm)
		if vm.Reserves() && (!info.done(in.Key) || newIP || newVM) {
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
		// An update's VMs join those of their component it keeps.
		for _, b := range earlier {
			if b.vm.Component == c.Key {
				in.VMs, in.Morefs = append(in.VMs, b.vm.Name), append(in.Morefs, b.moref)
			}
		}
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

	// 3. Customization and power on: the round's own VMs all have their
	// addresses.
	known := append(slices.Clone(earlier), built...)
	failed, err = eachVM(ctx, len(p.VMs), func(ctx workflow.Context, i int) (string, error) {
		vm := p.VMs[i]
		c, in := comps[vm.Component], input(vm, known)
		in.IP, in.Moref = built[i].ip, built[i].moref
		for _, step := range []struct {
			key string
			fn  any
		}{{KeyCustomize, a.Customize}, {KeyPowerOn, a.PowerOn}} {
			in.Key = keys.vm(c, step.key, vm)
			if info.done(in.Key) && !fresh[i] {
				continue
			}
			if err := workflow.ExecuteActivity(ctx, step.fn, in).Get(ctx, guestIP(vm, step.key, &in.IP)); err != nil {
				return in.Key, err
			}
		}
		built[i].ip = in.IP
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
