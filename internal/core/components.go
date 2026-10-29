package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/naming"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// VM blueprints by component (ADR-0024). A blueprint without components is
// one, whose VMs are each their own deployment.

// OneDeployment says whether a version's blueprint builds one deployment
// however many VMs: a VM blueprint with components (nil otherwise).
func OneDeployment(raw json.RawMessage) *bool {
	b, err := blueprint.Parse(raw)
	if err != nil || b.Kind != blueprint.KindVM || !b.Multi() {
		return nil
	}
	return present.Ptr(true)
}

// vmPart is one component, evaluated.
type vmPart struct {
	comp blueprint.Component
	// label starts its messages ("Web servers: "); empty for a single VM.
	label  string
	count  int
	vm     blueprint.VMPlan
	guest  blueprint.Guest
	rule   *store.PlacementRule
	subnet *store.NetworkSubnet
	// vcenter is its cluster's vCenter; chosen says rvav chose the cluster
	// or datastore (ADR-0024).
	vcenter string
	chosen  bool
	// nicSubnets are the added adapters' subnets (nil for DHCP).
	nicSubnets []*store.NetworkSubnet
	naming     *naming.Template
	prefix     string
	counter    string
	names      []string
	check      *nameCheck
	// addresses is the next free address of its network, as a hint.
	addresses []string
}

func (p vmPart) title() string { return cmp.Or(p.comp.Title, p.comp.Key) }

var bindingInput = regexp.MustCompile(`^\s*inputs\.([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// bindingField is the input a binding is (/inputs/<name>), so errors show
// next to it; def when the binding is anything else.
func bindingField(expr any, def string) string {
	if s, ok := expr.(string); ok {
		if m := bindingInput.FindStringSubmatch(s); m != nil {
			return "/inputs/" + m[1]
		}
	}
	return def
}

// evaluateVM evaluates a VM blueprint's components: placement, names,
// counts and addresses.
func (s *Service) evaluateVM(ctx context.Context, ev *Evaluation, strict bool) error {
	add := func(field, code, msg string) { ev.Errors = append(ev.Errors, fieldErr(field, code, msg)) }
	// A blueprint without sizes of its own uses the presets offered here
	// (ADR-0020).
	if len(ev.Blueprint.Sizes) == 0 {
		sizes, err := s.offeredSizes(ctx, ev.Project.ID, ev.Env.Key)
		if err != nil {
			return err
		}
		ev.Blueprint.Sizes = map[string]blueprint.Size{}
		for _, z := range sizes {
			ev.Blueprint.Sizes[z.Key] = blueprint.Size{Label: z.Label, VCPU: z.VCPU, MemoryGB: z.MemoryGB, DiskGB: z.DiskGB}
		}
	}
	pl, err := s.placement(ctx, ev.Project.ID, ev.Env.Key)
	if err != nil {
		return err
	}
	if len(pl.rules) == 0 && ev.Env.Key != "" {
		add("", "no_placement", fmt.Sprintf("%s has no placement for %s. Ask an administrator to add one.", ev.Project.Name, ev.Env.Name))
	}
	templates, err := s.Store.InventoryTemplates(ctx)
	if err != nil {
		return err
	}
	if ev.Blueprint.Multi() && ev.Count != 1 {
		add("/count", "out_of_range", ev.Item.Name+" builds one deployment: how many of each VM is in its form.")
		ev.Count = 1
	}
	given := map[string]bool{} // names given to earlier components
	ev.Parts, ev.VMCount = nil, 0
	for _, c := range ev.Blueprint.ComponentList() {
		part, err := s.evaluatePart(ctx, ev, pl, c, templates, given, strict)
		if err != nil {
			return err
		}
		ev.Parts = append(ev.Parts, part)
		ev.VMCount += part.count
	}
	if ev.Blueprint.Multi() {
		switch {
		case ev.VMCount > blueprint.MaxVMs:
			add("", "out_of_range", fmt.Sprintf("This would build %d VMs; a request builds at most %d.", ev.VMCount, blueprint.MaxVMs))
		case ev.VMCount == 0:
			add("", "out_of_range", "Every component has a count of 0: there would be nothing to build.")
		}
	}
	// The first component is what single-VM code and policies read.
	f := ev.Parts[0]
	ev.VM, ev.Rule, ev.Subnet, ev.Blueprint.Guest = f.vm, f.rule, f.subnet, f.guest
	ev.Naming, ev.NamePrefix, ev.CounterKey, ev.names, ev.Addresses = f.naming, f.prefix, f.counter, f.check, f.addresses
	ev.Names = nil
	for _, p := range ev.Parts {
		if p.names != nil {
			ev.Names = append(ev.Names, p.names...)
		}
	}
	return nil
}

// evaluatePart evaluates one component. given holds the names earlier
// components were given, which it adds its own to.
func (s *Service) evaluatePart(ctx context.Context, ev *Evaluation, pl *placement, c blueprint.Component, templates []store.InvTemplate,
	given map[string]bool, strict bool) (vmPart, error) {
	multi := ev.Blueprint.Multi()
	part := vmPart{comp: c, guest: *c.Guest, count: ev.Count}
	if multi {
		part.label = part.title() + ": "
	}
	add := func(field, code, msg string) { ev.Errors = append(ev.Errors, fieldErr(field, code, part.label+msg)) }
	// A single VM's placement errors show by its usual inputs; a
	// component's by the input it binds, else on the form.
	field := func(key string) string {
		def := ""
		if !multi {
			def = "/inputs/" + key
		}
		return bindingField(c.Bindings[key], def)
	}
	plan, count, missing := ev.Blueprint.EvaluateComponent(c, ev.Inputs)
	if multi {
		part.count = count
		switch {
		case slices.Contains(missing, "count"):
			part.count = 1 // shown as one until the inputs it needs are given
			if strict {
				add(bindingField(c.Count, ""), "invalid", "How many VMs could not be worked out from the inputs.")
			}
		case count < 0 || count > blueprint.MaxVMs:
			add(bindingField(c.Count, ""), "out_of_range", fmt.Sprintf("From 0 to %d VMs.", blueprint.MaxVMs))
			part.count = max(0, min(count, blueprint.MaxVMs))
		}
	}
	// A template chosen in the form is an offered template's key; a
	// blueprint that names its own vCenter template keeps it. An offered
	// template's customization specification is the guest's base, unless
	// the blueprint names one (ADR-0024).
	if plan.Template != "" {
		offered, err := s.offeredTemplates(ctx, ev.Project.ID, ev.Env.Key)
		if err != nil {
			return part, err
		}
		for _, t := range offered {
			if c.Bindings["template"] != "" && t.Key == plan.Template {
				plan.Template, plan.Library, part.guest.OSFamily = t.VCenterTemplate, t.ContentLibrary, t.OSFamily
			}
			if t.VCenterTemplate == plan.Template && part.guest.CustomizationSpec == "" && t.Customization != "" {
				part.guest.CustomizationSpec = t.Customization
			}
		}
	}
	part.vm = plan
	if part.count == 0 {
		return part, nil // nothing to place or name
	}
	// A size the bindings can't work out is the blueprint's fault once the
	// inputs are all there: never a VM of the template's size by default.
	if strict && (plan.VCPU < 1 || plan.MemoryGB < 1) {
		add("", "invalid_size", "The VMs' CPU and memory could not be worked out from the inputs. Ask a catalog admin to check this item.")
	}
	// Added disks and adapters too: never a disk of 0 GB or an adapter on
	// no network.
	for _, m := range missing {
		if strict && (strings.HasPrefix(m, "disks/") || strings.HasPrefix(m, "nics/") || m == "tags") {
			add("", "invalid", m+" could not be worked out from the inputs. Ask a catalog admin to check this item.")
		}
	}
	for i, d := range plan.Disks {
		if !slices.Contains(missing, fmt.Sprintf("disks/%d/size_gb", i)) && (d.SizeGB < 1 || d.SizeGB > 65536) {
			add(bindingField(c.Disks[i].SizeGB, ""), "out_of_range", fmt.Sprintf("A disk of %d GB: from 1 to 65536 GB.", d.SizeGB))
		}
	}

	// Placement chooses the cluster, and the datastore, the blueprint
	// doesn't bind (ADR-0024).
	pickDatastore := plan.Datastore == "" && c.Bindings["datastore"] == ""
	if plan.Cluster == "" && c.Bindings["cluster"] == "" && len(pl.rules) > 0 {
		// Only in a vCenter that has the template (when the inventory says).
		hasTemplate := func(vc string) bool {
			return len(templates) == 0 || slices.ContainsFunc(templates, func(t store.InvTemplate) bool {
				return t.Name == plan.Template && t.Library == plan.Library && t.VCenter == vc
			})
		}
		if cl := pl.choose(plan, part.count, c.AntiAffinity, pickDatastore, hasTemplate); cl != nil {
			plan.Cluster, part.chosen = cl.Name, true
		} else {
			add("", "no_room", "No cluster allowed here has "+orDefault(plan.Network, "the network")+" and room for these VMs. Ask an administrator.")
		}
	}
	if pickDatastore && plan.Cluster != "" && pl.cluster(plan.Cluster) != nil {
		if ds := pl.roomiest(plan.Cluster, plan.StorageGB()*part.count); ds != "" {
			plan.Datastore, part.chosen = ds, true
		} else {
			add("", "no_room", fmt.Sprintf("No datastore allowed on %s has room for %d GB. Ask an administrator.", plan.Cluster, plan.StorageGB()*part.count))
		}
	}
	if plan.Cluster != "" {
		if pl.cluster(plan.Cluster) == nil {
			add(field("cluster"), "not_allowed", fmt.Sprintf("Cluster %s is not available to %s in %s.", plan.Cluster, ev.Project.Name, ev.Env.Name))
		} else {
			part.rule, part.vcenter = pl.rule(plan.Cluster), pl.cluster(plan.Cluster).VCenter
			if pl.ambiguous(plan.Cluster) {
				add(field("cluster"), "ambiguous", fmt.Sprintf("Cluster %s is in two vCenters. Ask an administrator to name the vCenter in the placement rule.", plan.Cluster))
			}
			if plan.Datastore != "" && !slices.ContainsFunc(pl.datastoresFor(plan.Cluster), func(d store.InvDatastore) bool { return d.Name == plan.Datastore }) {
				add(field("datastore"), "not_allowed", fmt.Sprintf("Datastore %s cannot be used on %s.", plan.Datastore, plan.Cluster))
			}
			if plan.Network != "" && !slices.ContainsFunc(pl.networksFor(plan.Cluster), func(n store.InvNetwork) bool { return n.Name == plan.Network }) {
				add(field("network"), "not_allowed", fmt.Sprintf("Network %s cannot be used on %s.", plan.Network, plan.Cluster))
			}
			if cl := pl.cluster(plan.Cluster); c.AntiAffinity && part.count > 1 && cl.HostCount > 0 && part.count > cl.HostCount {
				add(field("cluster"), "too_few_hosts", fmt.Sprintf("%d VMs kept on separate hosts need %d hosts; %s has %d.",
					part.count, part.count, plan.Cluster, cl.HostCount))
			}
		}
	}
	if plan.Network != "" {
		if sn, ok := pl.subnets[plan.Network]; ok {
			part.subnet = &sn
		} else {
			add(field("network"), "no_subnet", "No EfficientIP subnet is linked to "+plan.Network+". Ask an administrator to link one.")
		}
	}
	// Added disks and adapters are placed with the VM (ADR-0024).
	for i, d := range plan.Disks {
		if d.Datastore != "" && plan.Cluster != "" && pl.cluster(plan.Cluster) != nil &&
			!slices.ContainsFunc(pl.datastoresFor(plan.Cluster), func(x store.InvDatastore) bool { return x.Name == d.Datastore }) {
			add(bindingField(c.Disks[i].Datastore, ""), "not_allowed", fmt.Sprintf("Datastore %s cannot be used on %s.", d.Datastore, plan.Cluster))
		}
	}
	part.nicSubnets = make([]*store.NetworkSubnet, len(plan.NICs))
	for i, n := range plan.NICs {
		at := bindingField(c.NICs[i].Network, "")
		if n.Network == "" {
			continue
		}
		if plan.Cluster != "" && pl.cluster(plan.Cluster) != nil &&
			!slices.ContainsFunc(pl.networksFor(plan.Cluster), func(x store.InvNetwork) bool { return x.Name == n.Network }) {
			add(at, "not_allowed", fmt.Sprintf("Network %s cannot be used on %s.", n.Network, plan.Cluster))
		}
		if n.IPAM != blueprint.IPAMEfficientIP {
			continue
		}
		if sn, ok := pl.subnets[n.Network]; ok {
			part.nicSubnets[i] = &sn
		} else {
			add(at, "no_subnet", "No EfficientIP subnet is linked to "+n.Network+". Ask an administrator to link one.")
		}
	}
	// In the vCenter the VMs go to, once their cluster is known.
	inVC := func(vc string) bool { return part.vcenter == "" || vc == part.vcenter }
	if len(templates) > 0 && plan.Template != "" && !slices.ContainsFunc(templates, func(t store.InvTemplate) bool {
		return t.Name == plan.Template && t.Library == plan.Library && inVC(t.VCenter)
	}) {
		what := "Template " + plan.Template
		if plan.Library != "" {
			what += " of content library " + plan.Library
		}
		add("", "template_missing", what+" was not found in vCenter. Ask a catalog admin to check this item.")
	}
	// The storage policy: the component's, else its placement rule's; a
	// disk's own wins (ADR-0024).
	if plan.StoragePolicy == "" && part.rule != nil {
		plan.StoragePolicy = part.rule.StoragePolicy
	}
	if policies, err := s.Store.InventoryStoragePolicies(ctx); err != nil {
		return part, err
	} else if len(policies) > 0 {
		known := func(name string) bool {
			return name == "" || slices.ContainsFunc(policies, func(p store.InvStoragePolicy) bool { return p.Name == name && inVC(p.VCenter) })
		}
		if !known(plan.StoragePolicy) {
			add("", "storage_policy_missing", fmt.Sprintf("Storage policy %s was not found in vCenter. Ask an administrator to check it.", plan.StoragePolicy))
		}
		for _, d := range plan.Disks {
			if !known(d.StoragePolicy) {
				add("", "storage_policy_missing", fmt.Sprintf("Storage policy %s was not found in vCenter. Ask a catalog admin to check this item.", d.StoragePolicy))
			}
		}
	}
	part.vm = plan

	spec, err := s.namingFor(ctx, ev.Project.ID, ev.Env.Key, c.Naming)
	if err != nil {
		return part, err
	}
	part.naming = &spec.Template
	vals := naming.Values(ev.Project.Code, ev.Env.Key, ev.Inputs)
	if multi {
		vals["component"] = c.Key
	}
	prefix, err := spec.Template.Prefix(vals)
	if err != nil {
		return part, nil // inputs used by the name are still missing; the schema reports them
	}
	part.prefix, part.counter = prefix, spec.counterKey(prefix)
	zone := ""
	if part.subnet != nil {
		zone = part.subnet.DNSZone
	}
	if part.names, _, part.check, err = s.peekNames(ctx, spec, prefix, zone, part.count, given); err != nil {
		return part, err
	}
	if err := part.check.err; err != nil {
		if strict {
			return part, err
		}
		s.Log.Warn("checking VM names in DNS", "err", err)
	}
	for _, n := range part.names {
		given[n] = true
	}
	if len(part.names) > 0 && !naming.Valid(part.names[0]) {
		def := ""
		if !multi {
			def = "/inputs/role"
		}
		add(def, "invalid_name", fmt.Sprintf("%s is not a valid host name.", part.names[0]))
	}

	if !strict && part.subnet != nil && s.IPAM != nil {
		if ip, err := s.IPAM.NextFree(ctx, subnetRef(*part.subnet)); err == nil {
			part.addresses = []string{ip}
		} else if errors.Is(err, efficientip.ErrSubnetFull) {
			add(field("network"), "subnet_full", fmt.Sprintf("Subnet %s has no free address. Pick another network.", part.subnet.Subnet))
		} else {
			s.Log.Warn("ipam next free address", "subnet", part.subnet.Subnet, "err", err)
		}
	}
	return part, nil
}

// choose is the cluster a component's VMs go to when the blueprint doesn't
// say: among those allowed in a vCenter with the template, with its
// network, enough hosts to keep the VMs apart, and a datastore with room
// (the one given, else any allowed), the one with the most free memory.
func (pl *placement) choose(plan blueprint.VMPlan, count int, apart, anyDatastore bool, hasTemplate func(vcenter string) bool) *store.InvCluster {
	var best *store.InvCluster
	for i := range pl.clusters {
		c := &pl.clusters[i]
		// A name in two vCenters is never chosen (its rule must say which),
		// and a name twice in one is the first.
		if pl.ambiguous(c.Name) || pl.cluster(c.Name) != c || !hasTemplate(c.VCenter) {
			continue
		}
		if plan.Network != "" && !slices.ContainsFunc(pl.networksFor(c.Name), func(n store.InvNetwork) bool { return n.Name == plan.Network }) {
			continue
		}
		if apart && count > 1 && c.HostCount > 0 && c.HostCount < count {
			continue
		}
		room := false
		for _, d := range pl.datastoresFor(c.Name) {
			if anyDatastore && d.FreeGB >= int64(plan.StorageGB()*count) || !anyDatastore && d.Name == plan.Datastore {
				room = true
			}
		}
		if !room {
			continue
		}
		if best == nil || c.MemoryTotalMB-c.MemoryUsedMB > best.MemoryTotalMB-best.MemoryUsedMB {
			best = c
		}
	}
	return best
}

// roomiest is the allowed datastore of the cluster with the most free
// space, if it has room for need GB ("" when none has).
func (pl *placement) roomiest(cluster string, need int) string {
	best, free := "", int64(-1)
	for _, d := range pl.datastoresFor(cluster) {
		if d.FreeGB >= int64(need) && d.FreeGB > free {
			best, free = d.Name, d.FreeGB
		}
	}
	return best
}

// vmQuota is what a VM blueprint's components add to the quotas.
func (ev *Evaluation) vmQuota() map[string]int {
	q := map[string]int{"vcpu": 0, "memory_gb": 0, "storage_gb": 0, "vm_count": 0}
	for _, p := range ev.Parts {
		q["vcpu"] += p.vm.VCPU * p.count
		q["memory_gb"] += p.vm.MemoryGB * p.count
		q["storage_gb"] += p.vm.StorageGB() * p.count
		q["vm_count"] += p.count
	}
	return q
}

// allParts are the components of a VM item, or of a composite's VM steps.
func (ev *Evaluation) allParts() []vmPart {
	parts := ev.Parts
	for _, c := range ev.Composite {
		if c.sub != nil {
			parts = append(parts, c.sub.Parts...)
		}
	}
	return parts
}

// vmPolicy is what approval policies see as vm: each size the largest of
// the VMs built (a component of none counts for nothing), and how many
// VMs in all.
func (ev *Evaluation) vmPolicy() map[string]any {
	vcpu, mem, disk, found := 0, 0, 0, false
	for _, p := range ev.allParts() {
		if p.count > 0 {
			vcpu, mem, disk, found = max(vcpu, p.vm.VCPU), max(mem, p.vm.MemoryGB), max(disk, p.vm.DiskGB), true
		}
	}
	if !found {
		vcpu, mem, disk = ev.VM.VCPU, ev.VM.MemoryGB, ev.VM.DiskGB
	}
	return map[string]any{"vcpu": vcpu, "memory_gb": mem, "disk_gb": disk, "count": max(ev.Count, ev.VMCount)}
}

// vmVars are the VMs approval policies see in vms, one entry each.
func (ev *Evaluation) vmVars() []any {
	parts := ev.allParts()
	out := []any{}
	for _, p := range parts {
		for range p.count {
			out = append(out, map[string]any{"component": p.comp.Key, "vcpu": p.vm.VCPU, "memory_gb": p.vm.MemoryGB,
				"storage_gb": p.vm.StorageGB()})
		}
	}
	return out
}

// previewComponents says what each component builds.
func (ev *Evaluation) previewComponents() []api.PreviewComponent {
	out := []api.PreviewComponent{}
	str := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	for _, p := range ev.Parts {
		c := api.PreviewComponent{Key: p.comp.Key, Title: p.title(), Count: p.count, Names: nonNilStrings(p.names),
			Vcpu: p.vm.VCPU, MemoryGb: p.vm.MemoryGB, StorageGb: p.vm.StorageGB(), Template: str(p.vm.Template),
			AntiAffinity: p.comp.AntiAffinity && p.count > 1, Tags: present.Ptr(nonNilStrings(p.vm.Tags))}
		if !ev.Blueprint.Multi() {
			c.Title = ev.Item.Name
		}
		if p.guest.OSFamily == "linux" || p.guest.OSFamily == "windows" {
			c.OsFamily = present.Ptr(api.PreviewComponentOsFamily(p.guest.OSFamily))
		}
		c.Placement = &struct {
			Chosen        *bool   `json:"chosen,omitempty"`
			Cluster       *string `json:"cluster,omitempty"`
			Datastore     *string `json:"datastore,omitempty"`
			Folder        *string `json:"folder,omitempty"`
			StoragePolicy *string `json:"storage_policy,omitempty"`
			Vcenter       *string `json:"vcenter,omitempty"`
		}{Cluster: str(p.vm.Cluster), Datastore: str(p.vm.Datastore), StoragePolicy: str(p.vm.StoragePolicy), Vcenter: str(p.vcenter)}
		if p.chosen {
			c.Placement.Chosen = present.Ptr(true)
		}
		if p.rule != nil {
			c.Placement.Folder = str(p.rule.Folder)
		}
		type disk = struct {
			Datastore     *string `json:"datastore,omitempty"`
			SizeGb        int     `json:"size_gb"`
			StoragePolicy *string `json:"storage_policy,omitempty"`
		}
		c.Disks = []disk{{SizeGb: p.vm.DiskGB, Datastore: str(p.vm.Datastore), StoragePolicy: str(p.vm.StoragePolicy)}}
		for _, d := range p.vm.Disks {
			c.Disks = append(c.Disks, disk{SizeGb: d.SizeGB, Datastore: str(cmp.Or(d.Datastore, p.vm.Datastore)),
				StoragePolicy: str(cmp.Or(d.StoragePolicy, p.vm.StoragePolicy))})
		}
		type nic = struct {
			Address *string                      `json:"address,omitempty"`
			Ipam    api.PreviewComponentNicsIpam `json:"ipam"`
			Key     string                       `json:"key"`
			Network string                       `json:"network"`
			Subnet  *string                      `json:"subnet,omitempty"`
		}
		first := nic{Key: "nic1", Network: p.vm.Network, Ipam: api.PreviewComponentNicsIpam(blueprint.IPAMEfficientIP)}
		if p.subnet != nil {
			first.Subnet = str(p.subnet.Subnet)
		}
		if len(p.addresses) > 0 {
			first.Address = &p.addresses[0]
		}
		c.Nics = []nic{first}
		for i, n := range p.vm.NICs {
			x := nic{Key: n.Key, Network: n.Network, Ipam: api.PreviewComponentNicsIpam(n.IPAM)}
			if i < len(p.nicSubnets) && p.nicSubnets[i] != nil {
				x.Subnet = str(p.nicSubnets[i].Subnet)
			}
			c.Nics = append(c.Nics, x)
		}
		out = append(out, c)
	}
	return out
}

// deploymentName is a multi-VM deployment's name: its blueprint's
// deployment_name, else the item's name and the request number.
func deploymentName(ev *Evaluation, number string) string {
	if raw := ev.Blueprint.DeploymentName; raw != "" {
		if t, err := naming.ParseLabel(raw); err == nil {
			if name, err := t.Prefix(naming.Values(ev.Project.Code, ev.Env.Key, ev.Inputs)); err == nil && name != "" {
				return name
			}
		}
	}
	return ev.Item.Name + " " + number
}

// vmUnit is the plan for one VM of a component.
func vmUnit(ev *Evaluation, p vmPart, deploymentID, name string, index int) provision.VMUnit {
	vm := provision.VMUnit{
		DeploymentID: deploymentID, Component: p.comp.Key, Index: index, Name: name,
		VCPU: p.vm.VCPU, MemoryGB: p.vm.MemoryGB, DiskGB: p.vm.DiskGB,
		Cluster: p.vm.Cluster, Datastore: p.vm.Datastore, Network: p.vm.Network, Template: p.vm.Template,
		Library: p.vm.Library, StoragePolicy: p.vm.StoragePolicy, VCenter: p.vcenter,
	}
	if p.rule != nil {
		vm.Folder = p.rule.Folder
	}
	if sn := p.subnet; sn != nil {
		vm.FQDN = name + "." + sn.DNSZone
		vm.Subnet, vm.SubnetID, vm.Gateway = sn.Subnet, sn.EfficientIPSubnetID, sn.Gateway
		vm.PrefixLength, vm.DNSZone, vm.DNSServers = prefixLength(sn.Subnet), sn.DNSZone, sn.DNSServers
	}
	for _, d := range p.vm.Disks {
		d.Datastore = cmp.Or(d.Datastore, p.vm.Datastore)
		vm.Disks = append(vm.Disks, d)
	}
	for i, n := range p.vm.NICs {
		nu := provision.NICUnit{Key: n.Key, Network: n.Network, IPAM: n.IPAM}
		if i < len(p.nicSubnets) && p.nicSubnets[i] != nil {
			sn := p.nicSubnets[i]
			// <name>-<key> in its subnet's zone.
			nu.FQDN = name + "-" + n.Key + "." + sn.DNSZone
			nu.Subnet, nu.SubnetID, nu.PrefixLength = sn.Subnet, sn.EfficientIPSubnetID, prefixLength(sn.Subnet)
			nu.DNSZone, nu.DNSServers = sn.DNSZone, sn.DNSServers
		}
		vm.NICs = append(vm.NICs, nu)
	}
	vm.Tags = p.vm.Tags
	return vm
}

// postConfigJobs lists the AWX jobs to run on a component's VMs after
// boot, in the form's order.
func postConfigJobs(ev *Evaluation, plan blueprint.VMPlan, order []string) ([]provision.Job, error) {
	var jobs []provision.Job
	for _, key := range ev.Blueprint.OrderedPostConfig(plan.PostConfig, order) {
		t, ok := ev.Blueprint.AWXTemplates[key]
		if !ok {
			return nil, &ValidationError{Errors: []api.FieldError{fieldErr("/inputs/post_config", "unknown_template",
				fmt.Sprintf("%s is not configured for this item.", key))}}
		}
		jobs = append(jobs, provision.Job{Key: key, Title: t.Title, Template: t.JobTemplate, Workflow: t.Workflow})
	}
	return jobs, nil
}

// lockPrefixes locks the name prefixes a request gives names from, its
// composite's steps' too, in one order, so two requests never wait on each
// other. Locks taken again by the same transaction stack.
func (s *Service) lockPrefixes(ctx context.Context, tx pgx.Tx, ev *Evaluation) error {
	var prefixes []string
	for _, p := range ev.allParts() {
		if p.count > 0 && p.prefix != "" && !slices.Contains(prefixes, p.prefix) {
			prefixes = append(prefixes, p.prefix)
		}
	}
	slices.Sort(prefixes)
	for _, prefix := range prefixes {
		if err := s.Store.LockNamePrefix(ctx, tx, prefix); err != nil {
			return err
		}
	}
	return nil
}

// componentUnit is a component's plan: its guest, anti-affinity and jobs.
func componentUnit(ev *Evaluation, p vmPart, order []string) (provision.ComponentUnit, error) {
	jobs, err := postConfigJobs(ev, p.vm, order)
	return provision.ComponentUnit{Key: p.comp.Key, Title: p.title(), Guest: p.guest,
		AntiAffinity: p.comp.AntiAffinity && p.count > 1, PostConfig: jobs}, err
}

// replanVMs plans a request's VMs again after an approver changed its
// inputs: they keep their names and deployments, and a multi-VM request
// how many of each component it builds.
func replanVMs(ev *Evaluation, plan *provision.Plan) error {
	parts := map[string]vmPart{}
	for _, p := range ev.Parts {
		parts[p.comp.Key] = p
	}
	counts := map[string]int{}
	for i, vm := range plan.VMs {
		key := cmp.Or(vm.Component, blueprint.SingleKey)
		p, ok := parts[key]
		if !ok {
			return &ValidationError{Errors: []api.FieldError{fieldErr("", "not_allowed", "The item no longer has the component "+key+".")}}
		}
		counts[key]++
		plan.VMs[i] = vmUnit(ev, p, vm.DeploymentID, vm.Name, vm.Index)
	}
	order := optionOrder(ev.Version.UISchema, "post_config")
	if !ev.Blueprint.Multi() {
		var err error
		plan.PostConfig, err = postConfigJobs(ev, ev.VM, order)
		return err
	}
	plan.Components = nil
	for _, p := range ev.Parts {
		if counts[p.comp.Key] != p.count {
			return &ValidationError{Errors: []api.FieldError{fieldErr(bindingField(p.comp.Count, ""), "not_allowed",
				fmt.Sprintf("%s: how many VMs it builds (%d) can't change once requested.", p.title(), counts[p.comp.Key]))}}
		}
		if p.count == 0 {
			continue
		}
		c, err := componentUnit(ev, p, order)
		if err != nil {
			return err
		}
		plan.Components = append(plan.Components, c)
	}
	return nil
}

// reserveVMs names a VM item's VMs for good, inside the submit
// transaction, and makes their deployments: one per VM, or one for every
// component's VMs. It returns the multi-VM plan's components too.
func (s *Service) reserveVMs(ctx context.Context, tx pgx.Tx, ev *Evaluation, requestID, owner, number string) (
	[]store.Deployment, []provision.VMUnit, []provision.ComponentUnit, error) {
	multi := ev.Blueprint.Multi()
	newDeployment := func(name string) store.Deployment {
		dep := store.Deployment{
			ID: ids.New(ids.Deployment), Name: name, Status: "provisioning", ProjectID: ev.Project.ID,
			Environment: ev.Env.Key, ItemID: ev.Item.ID, VersionID: ev.Version.ID, OwnerID: owner,
			CreatedByRequestID: &requestID, LeaseExpiresAt: ev.Lease.ExpiresAt, ExpiryAction: orDefault(ev.Lease.ExpiryAction, "notify_only"),
		}
		if ev.Lease.ExpiresAt != nil {
			dep.LeaseExtensionsMax = &ev.Lease.MaxExtensions
		}
		dep.Inputs, _ = json.Marshal(ev.Inputs)
		return dep
	}
	if err := s.lockPrefixes(ctx, tx, ev); err != nil {
		return nil, nil, nil, err
	}
	var deps []store.Deployment
	var vms []provision.VMUnit
	var comps []provision.ComponentUnit
	if multi {
		deps = append(deps, newDeployment(deploymentName(ev, number)))
	}
	given := map[string]bool{}
	order := optionOrder(ev.Version.UISchema, "post_config")
	for _, p := range ev.Parts {
		if p.count == 0 {
			continue
		}
		// Read in the transaction: it holds the prefix's lock, and others
		// waiting for it may hold every other connection (S14.7).
		taken, err := s.Store.InventoryVMNames(ctx, tx, p.prefix)
		if err != nil {
			return nil, nil, nil, err
		}
		ours, err := s.Store.DeploymentNames(ctx, tx, p.prefix)
		if err != nil {
			return nil, nil, nil, err
		}
		for n := range given {
			ours[n] = true
		}
		zone := ""
		if p.subnet != nil {
			zone = p.subnet.DNSZone
		}
		names := &nameCheck{ctx: ctx, ipam: s.IPAM, vcenter: taken, rvav: ours, zone: zone}
		if p.check != nil {
			names.dns = p.check.dns
		}
		for i := range p.count {
			seq, err := s.Store.NextName(ctx, tx, p.counter, func(n int) bool { return names.taken(p.naming.Name(p.prefix, n)) })
			if err != nil {
				return nil, nil, nil, err
			}
			if names.err != nil {
				return nil, nil, nil, names.err
			}
			name := p.naming.Name(p.prefix, seq)
			// Names given in this request count as taken (components, or a
			// composite with two VM steps, of one naming).
			names.rvav[name], given[name] = true, true
			if !multi {
				deps = append(deps, newDeployment(name))
			}
			vms = append(vms, vmUnit(ev, p, deps[len(deps)-1].ID, name, i))
		}
		if multi {
			c, err := componentUnit(ev, p, order)
			if err != nil {
				return nil, nil, nil, err
			}
			comps = append(comps, c)
		}
	}
	return deps, vms, comps, nil
}
