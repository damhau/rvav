package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/naming"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/secretinput"
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

// Topology is what a request form draws of what the item builds, without
// what only its administrators see (ADR-0032); nil for AWX and workflow
// items.
func Topology(raw json.RawMessage) *map[string]any {
	if t := blueprint.Topology(raw); t != nil {
		return &t
	}
	return nil
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
	// nicSubnets are the added adapters' subnets (nil for DHCP), and
	// nicSegments the keys of the segments made for the deployment they are
	// on ("" for an existing network).
	nicSubnets  []*store.NetworkSubnet
	nicSegments []string
	// segment is the key of the NSX segment made for the deployment its
	// VMs' network is, and segments where the placement rule makes them
	// (ADR-0028).
	segment  string
	segments *store.PlacementSegments
	naming   *naming.Template
	prefix   string
	counter  string
	names    []string
	check    *nameCheck
	// addresses is the next free address of its network, as a hint.
	addresses []string
	// cloned is the catalogue template its VMs are cloned from: the one
	// the form chose, or the one with the blueprint's template as a
	// source; nil for none.
	cloned *store.VMTemplate
	// constraints are what its constraints did (ADR-0035).
	constraints []constraintResult
	// hosts are those its VMs start on when narrowed; pod is the datastore
	// cluster its datastore is in, whose podDatastores Storage DRS may
	// choose from when sdrs; strategy is what chose its cluster, and
	// strategyNote what that weighed when it isn't the default (ADR-0036).
	hosts         []string
	pod           string
	podDatastores []string
	sdrs          bool
	strategy      string
	strategyNote  string
	// image is the catalogue template the form chose, and imageExcluded
	// the vCenters it can't be cloned in (ADR-0037).
	image         *store.VMTemplate
	imageExcluded []string
	// seen is what other requests' VMs not built yet held on the cluster
	// the strategy chose, when it chose it (ADR-0036).
	seen *reservedSeen
	// linkedClone: its VMs are linked clones of the template's current
	// snapshot, as the blueprint says, else the catalogue template.
	linkedClone bool
}

func (p vmPart) title() string { return cmp.Or(p.comp.Title, p.comp.Key) }

// zone is the DNS zone its VMs are named in: their subnet's, or that of
// the segments made for the deployment.
func (p vmPart) zone() string {
	switch {
	case p.subnet != nil:
		return p.subnet.DNSZone
	case p.segment != "" && p.segments != nil:
		return p.segments.DNSZone
	}
	return ""
}

var bindingInput = regexp.MustCompile(`^\s*inputs\.([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// bindingField is the input a binding is (/inputs/<name>), so errors show
// next to it; def when the binding is anything else.
// inputOf is the field of the first input a component's text reads, ""
// when it reads none: where an error about that text shows.
func inputOf(text string) string {
	if m := readsInput.FindStringSubmatch(text); m != nil {
		return "/inputs/" + m[1]
	}
	return ""
}

var readsInput = regexp.MustCompile(`\binputs\.([A-Za-z_][A-Za-z0-9_]*)`)

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
	var self string
	if ev.edit != nil {
		self = ev.edit.requestID
	}
	if err := s.withReservations(ctx, pl, self); err != nil {
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
	if err := s.evaluateNSXGroups(ctx, ev); err != nil {
		return err
	}
	if err := s.evaluateResources(ctx, ev); err != nil {
		return err
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
	for _, k := range ev.Blueprint.SecretProperties(c, ev.Inputs, present.SecretFields(ev.Version.UISchema)) {
		plan.Properties[k] = hidden
	}
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
	// A template chosen in the form is an offered template's key, cloned
	// from its source in the vCenter placement chooses (ADR-0037); a
	// blueprint that names its own vCenter template keeps it. An offered
	// template's customization specification is the guest's base, unless
	// the blueprint names one (ADR-0024).
	if plan.Template != "" {
		offered, err := s.offeredTemplates(ctx, ev.Project.ID, ev.Env.Key)
		if err != nil {
			return part, err
		}
		if i := slices.IndexFunc(offered, func(t store.VMTemplate) bool { return t.Key == plan.Template }); c.Bindings["template"] != "" && i >= 0 {
			part.image = &offered[i]
			part.guest.OSFamily = part.image.OSFamily
			if len(part.image.Sources) > 0 {
				plan.Template, plan.Library = part.image.Sources[0].Template, part.image.Sources[0].Library
			}
		}
		for _, t := range offered {
			ours := part.image != nil && t.Key == part.image.Key || part.image == nil && hasSource(t, plan.Template, plan.Library)
			if ours && part.guest.CustomizationSpec == "" && t.Customization != "" {
				part.guest.CustomizationSpec = t.Customization
			}
			if ours && part.cloned == nil {
				part.cloned = &t
			}
		}
		if part.image != nil && part.image.UserData != "" && part.guest.CustomizationSpec != "" && part.guest.OSFamily != "windows" {
			add(field("template"), "invalid", fmt.Sprintf("%s configures its VMs with cloud-init, and this item names the customization specification %s. Ask a catalog admin to check this item.",
				part.image.Label, part.guest.CustomizationSpec))
		}
	}
	if msg := accessProblem(part.guest, part.cloned); msg != "" {
		add(field("template"), "invalid", msg)
	}
	part.linkedClone = part.image != nil && part.image.LinkedClone
	if c.LinkedClone != nil {
		part.linkedClone = *c.LinkedClone
	}
	part.vm = plan
	if part.count == 0 {
		return part, nil // nothing to place or name
	}
	// Networks made for the deployment: NSX segments, on the Tier-1 of the
	// first placement rule that makes them (ADR-0028), for its first adapter
	// or added ones. Only the item's own segments: never one an input names.
	segmentOf := func(network, at string) string {
		key, ok := blueprint.SegmentKey(network)
		if !ok {
			return ""
		}
		if !slices.ContainsFunc(ev.Blueprint.Segments, func(sg blueprint.Segment) bool { return sg.Key == key }) {
			add(at, "unknown_segment", fmt.Sprintf("%q is none of the item's segments.", key))
			return ""
		}
		return key
	}
	part.segment = segmentOf(plan.Network, field("network"))
	if part.segment != "" && plan.DHCP {
		add(field("network"), "not_allowed", "A segment made for the deployment has no DHCP: its VMs get their addresses from EfficientIP.")
	}
	if part.segment != "" && plan.IPv6 {
		add(field("network"), "not_allowed", "A segment made for the deployment has IPv4 only: its subnet is carved from an IPv4 block.")
	}
	// Where a missing placement rule for segments is said: the first adapter
	// on one.
	needsSegments, segmentAt := part.segment != "", field("network")
	part.nicSegments = make([]string, len(plan.NICs))
	for i, n := range plan.NICs {
		at := bindingField(c.NICs[i].Network, "")
		if key := segmentOf(n.Network, at); key != "" {
			if n.IPAM != blueprint.IPAMEfficientIP {
				// rvav's segments have no DHCP: their VMs' addresses are static.
				add(at, "not_allowed", "A segment made for the deployment has no DHCP: its adapters get their addresses from EfficientIP.")
				continue
			}
			part.nicSegments[i] = key
			if !needsSegments {
				needsSegments, segmentAt = true, at
			}
		}
	}
	if needsSegments {
		for _, r := range pl.rules {
			if r.Segments != nil {
				sg := *r.Segments
				part.segments = &sg
				break
			}
		}
		if part.segments == nil && len(pl.rules) > 0 {
			add(segmentAt, "no_segments", fmt.Sprintf("No placement rule of %s in %s makes NSX segments. Ask an administrator to add one.",
				ev.Project.Name, ev.Env.Name))
		}
	}
	// Its port group doesn't exist until the request makes it: clusters are
	// chosen, and checked, without it.
	placed := plan
	if _, ok := blueprint.SegmentKey(plan.Network); ok {
		placed.Network = "" // refused above if it isn't the item's
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

	// Placement chooses the cluster, the datastore, and the disks'
	// datastores, the blueprint doesn't bind (ADR-0024), and its network
	// when constraints choose it, among what the constraints allow
	// (ADR-0035). A segment made for the deployment has no tags.
	cons := append(constraintsOf(ev, c, strict, add), imageConstraints(part.image)...)
	if part.segment != "" {
		cons = slices.DeleteFunc(cons, func(k blueprint.Constraint) bool { return k.Kind == blueprint.Network })
	}
	pickDatastore := plan.Datastore == "" && c.Bindings["datastore"] == ""
	pickNetwork := placed.Network == "" && part.segment == "" && c.Bindings["network"] == "" && len(cons.of(blueprint.Network, 0)) > 0
	// Added disks with constraints of their own and no datastore given go
	// where those choose; the VMs' datastore holds the rest.
	disks, vmGB := map[int]int{}, plan.StorageGB()
	for i, d := range plan.Disks {
		if d.Datastore == "" && c.Disks[i].Datastore == "" && len(cons.of(blueprint.Storage, i+1)) > 0 {
			disks[i+1], vmGB = d.SizeGB, vmGB-d.SizeGB
		}
	}
	var placedBy []constraintResult
	plan.ResourcePool = strings.Trim(plan.ResourcePool, "/")
	placed.ResourcePool = plan.ResourcePool
	// Only in a vCenter that has the template (when the inventory says):
	// for a catalogue template, its source there (ADR-0037).
	hasTemplate := func(vc string) bool {
		tpl, lib := plan.Template, plan.Library
		if part.image != nil {
			src, ok := sourceIn(*part.image, vc, pl.added)
			if !ok {
				return false
			}
			tpl, lib = src.Template, src.Library
		}
		return len(templates) == 0 || slices.ContainsFunc(templates, func(t store.InvTemplate) bool {
			return t.Name == tpl && t.Library == lib && t.VCenter == vc
		})
	}
	if plan.Cluster == "" && c.Bindings["cluster"] == "" && len(pl.rules) > 0 {
		noTemplate := ""
		if part.image != nil {
			for _, cl := range pl.clusters {
				if !hasTemplate(cl.VCenter) && !slices.Contains(part.imageExcluded, cl.VCenter) {
					part.imageExcluded = append(part.imageExcluded, cl.VCenter)
				}
			}
			noTemplate = fmt.Sprintf("%s has no template in %s now, where the clusters allowed here are. Ask an administrator.",
				part.image.Label, vcenterNames(part.imageExcluded))
		}
		ch, results, why := pl.choose(placementQuery{plan: placed, count: part.count, apart: c.AntiAffinity, anyDatastore: pickDatastore,
			anyNetwork: pickNetwork, hasTemplate: hasTemplate, noTemplate: noTemplate, cons: cons, disks: disks, vmGB: vmGB})
		placedBy = append(placedBy, results...)
		switch {
		case ch.cluster != nil:
			plan.Cluster, part.chosen = ch.cluster.Name, true
			part.chose(pl, ch.cluster)
			part.hosts, part.strategy, part.strategyNote = ch.hosts, ch.strategy, ch.note
		case why != "" && why == noTemplate:
			add("", "no_template", why)
		case why != "":
			add("", "constraint", why)
		case plan.Host != "":
			add(field("host"), "no_room", fmt.Sprintf("Host %s is none the rules allow with room for these VMs, or it takes no new VMs.", plan.Host))
		case plan.ResourcePool != "":
			add(field("resource_pool"), "no_room", fmt.Sprintf("No cluster allowed here has resource pool %s, or room for these VMs there.", plan.ResourcePool))
		case plan.DatastoreCluster != "":
			add(field("datastore_cluster"), "no_room", fmt.Sprintf("No cluster allowed here can use datastore cluster %s, or has room for these VMs on it.", plan.DatastoreCluster))
		default:
			add("", "no_room", "No cluster allowed here has "+orDefault(plan.Network, "the network")+" and room for these VMs. Ask an administrator.")
		}
	} else if cl := pl.cluster(plan.Cluster); cl != nil {
		hosts, results := pl.placeOnGiven(cl, plan.Host, cons.of(blueprint.Compute, 0), func(code, msg string) {
			if code == "not_allowed" {
				add(field("host"), code, msg)
			} else {
				add(field("cluster"), code, msg)
			}
		})
		part.hosts = hosts
		placedBy = append(placedBy, results...)
	}
	if plan.DatastoreCluster != "" && plan.Cluster != "" && pl.cluster(plan.Cluster) != nil {
		cl := pl.cluster(plan.Cluster)
		switch {
		case pl.pod(cl.VCenter, plan.DatastoreCluster) == nil:
			add(field("datastore_cluster"), "unknown", fmt.Sprintf("vCenter has no datastore cluster %s.", plan.DatastoreCluster))
		case len(pl.datastoresIn(plan.Cluster, plan.DatastoreCluster)) == 0:
			add(field("datastore_cluster"), "not_allowed", fmt.Sprintf("No datastore of %s can be used on %s.", plan.DatastoreCluster, plan.Cluster))
		case !pickDatastore && !slices.Contains(pl.pod(cl.VCenter, plan.DatastoreCluster).Datastores, plan.Datastore):
			add(field("datastore"), "not_allowed", fmt.Sprintf("Datastore %s is not in datastore cluster %s.", plan.Datastore, plan.DatastoreCluster))
		}
	}
	if pickDatastore && plan.Cluster != "" && pl.cluster(plan.Cluster) != nil {
		ds, results, why := pl.pickDatastore(plan.Cluster, plan.DatastoreCluster, vmGB*part.count, cons.of(blueprint.Storage, 0))
		placedBy = append(placedBy, results...)
		switch {
		case ds != "":
			plan.Datastore, part.chosen = ds, true
			// In a datastore cluster, Storage DRS may choose another of those
			// rvav would (ADR-0036).
			if part.pod = pl.podOf(plan.Cluster, plan.DatastoreCluster, ds); part.pod != "" {
				cands, _, _ := pl.datastoreCands(plan.Cluster, part.pod, vmGB*part.count, cons.of(blueprint.Storage, 0))
				for _, d := range cands {
					part.podDatastores = append(part.podDatastores, d.Name)
				}
				part.sdrs = pl.pod(pl.cluster(plan.Cluster).VCenter, part.pod).SDRS
			}
		case why != "":
			add("", "constraint", why)
		default:
			add("", "no_room", fmt.Sprintf("No datastore allowed on %s has room for %d GB. Ask an administrator.", plan.Cluster, vmGB*part.count))
		}
	} else if i := slices.IndexFunc(pl.datastoresFor(plan.Cluster), func(d store.InvDatastore) bool { return d.Name == plan.Datastore }); i >= 0 {
		d := pl.datastoresFor(plan.Cluster)[i]
		placedBy = append(placedBy, checkGiven(cons.of(blueprint.Storage, 0), "Datastore", d.Name, d.Tags,
			func(code, msg string) { add(field("datastore"), code, msg) })...)
	}
	if pickNetwork && plan.Cluster != "" && pl.cluster(plan.Cluster) != nil {
		n, results, why := pl.pickNetwork(plan.Cluster, cons.of(blueprint.Network, 0))
		placedBy = append(placedBy, results...)
		if n != "" {
			plan.Network, placed.Network, part.chosen = n, n, true
		} else {
			add("", "constraint", why)
		}
	} else if i := slices.IndexFunc(pl.networksFor(plan.Cluster), func(n store.InvNetwork) bool { return n.Name == placed.Network }); i >= 0 {
		n := pl.networksFor(plan.Cluster)[i]
		placedBy = append(placedBy, checkGiven(cons.of(blueprint.Network, 0), "Network", n.Name, n.Tags,
			func(code, msg string) { add(field("network"), code, msg) })...)
	}
	for i := range plan.Disks {
		ks := cons.of(blueprint.Storage, i+1)
		if len(ks) == 0 || plan.Cluster == "" || pl.cluster(plan.Cluster) == nil {
			continue
		}
		if gb, ok := disks[i+1]; ok {
			ds, results, why := pl.pickDatastore(plan.Cluster, plan.DatastoreCluster, gb*part.count, ks)
			placedBy = append(placedBy, results...)
			switch {
			case ds != "":
				plan.Disks[i].Datastore, part.chosen = ds, true
			case why != "":
				add("", "constraint", why)
			default:
				add("", "no_room", fmt.Sprintf("No datastore allowed on %s has room for disk %d (%d GB). Ask an administrator.", plan.Cluster, i+2, gb*part.count))
			}
		} else if j := slices.IndexFunc(pl.datastoresFor(plan.Cluster), func(d store.InvDatastore) bool { return d.Name == plan.Disks[i].Datastore }); j >= 0 {
			d := pl.datastoresFor(plan.Cluster)[j]
			placedBy = append(placedBy, checkGiven(ks, "Datastore", d.Name, d.Tags,
				func(code, msg string) { add(bindingField(c.Disks[i].Datastore, ""), code, msg) })...)
		}
	}
	part.constraints = placedBy
	if plan.Cluster != "" {
		if pl.cluster(plan.Cluster) == nil {
			add(field("cluster"), "not_allowed", fmt.Sprintf("Cluster %s is not available to %s in %s.", plan.Cluster, ev.Project.Name, ev.Env.Name))
		} else {
			part.rule, part.vcenter = pl.rule(plan.Cluster), pl.cluster(plan.Cluster).VCenter
			if part.image != nil {
				if src, ok := sourceIn(*part.image, part.vcenter, pl.added); ok {
					plan.Template, plan.Library = src.Template, src.Library
				} else {
					add(field("cluster"), "no_template", fmt.Sprintf("%s has no template in vCenter %s, where cluster %s is. Ask an administrator.",
						part.image.Label, part.vcenter, plan.Cluster))
					plan.Template, plan.Library = "", ""
				}
			}
			// A pool the rule names, or one below it (ADR-0036).
			if rp := part.rule.ResourcePool; rp != "" {
				switch {
				case plan.ResourcePool == "":
					plan.ResourcePool = rp
				case plan.ResourcePool != rp && !strings.HasPrefix(plan.ResourcePool, rp+"/"):
					add(field("resource_pool"), "not_allowed", fmt.Sprintf("Resource pool %s is not within %s, where VMs go on %s.", plan.ResourcePool, rp, plan.Cluster))
				}
			}
			if plan.ResourcePool != "" && pl.pool(*pl.cluster(plan.Cluster), plan.ResourcePool) == nil {
				add(field("resource_pool"), "not_allowed", fmt.Sprintf("Cluster %s has no resource pool %s.", plan.Cluster, plan.ResourcePool))
			}
			// vCenter clones only into a folder of the cluster's datacenter:
			// found later, the VM's addresses, DNS and folder would be made
			// first.
			if dc, at := blueprint.FolderDatacenter(plan.Folder), pl.cluster(plan.Cluster).Datacenter; dc != "" && at != "" && dc != at {
				add(inputOf(c.Folder), "not_allowed", fmt.Sprintf("Folder %s is in datacenter %s, and cluster %s in %s.",
					path.Clean(strings.TrimSpace(plan.Folder)), dc, plan.Cluster, at))
			}
			if pl.planned == nil {
				pl.planned, pl.plannedMB = map[string]int{}, map[string]int64{}
			}
			pl.planned[part.vcenter+"/"+plan.Cluster] += part.count
			pl.plannedMB[part.vcenter+"/"+plan.Cluster] += int64(part.count) * int64(plan.MemoryGB) * 1024
			if pl.ambiguous(plan.Cluster) {
				add(field("cluster"), "ambiguous", fmt.Sprintf("Cluster %s is in two vCenters. Ask an administrator to name the vCenter in the placement rule.", plan.Cluster))
			}
			if plan.Datastore != "" && !slices.ContainsFunc(pl.datastoresFor(plan.Cluster), func(d store.InvDatastore) bool { return d.Name == plan.Datastore }) {
				add(field("datastore"), "not_allowed", fmt.Sprintf("Datastore %s cannot be used on %s.", plan.Datastore, plan.Cluster))
			}
			if placed.Network != "" && !slices.ContainsFunc(pl.networksFor(plan.Cluster), func(n store.InvNetwork) bool { return n.Name == plan.Network }) {
				add(field("network"), "not_allowed", fmt.Sprintf("Network %s cannot be used on %s.", plan.Network, plan.Cluster))
			}
			if n := pl.hostCount(plan.Cluster, part.hosts); c.AntiAffinity && part.count > 1 && n > 0 && part.count > n {
				add(field("cluster"), "too_few_hosts", fmt.Sprintf("%d VMs kept on separate hosts need %d hosts; %s has %d.",
					part.count, part.count, plan.Cluster, n))
			}
		}
	}
	if placed.Network != "" && !plan.DHCP {
		if sn, ok := pl.subnets[plan.Network]; !ok {
			add(field("network"), "no_subnet", "No EfficientIP subnet is linked to "+plan.Network+". Ask an administrator to link one.")
		} else if part.subnet = &sn; plan.IPv6 && sn.Subnet6 == "" {
			add(field("network"), "no_subnet6", "No EfficientIP IPv6 subnet is linked to "+plan.Network+". Ask an administrator to link one.")
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
		// On a segment made for the deployment: its subnet is carved when the
		// request runs, its port group made then; refused above, no more said.
		if _, onSegment := blueprint.SegmentKey(n.Network); n.Network == "" || onSegment {
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
	if part.linkedClone && plan.Library != "" {
		add(field("template"), "invalid", fmt.Sprintf("Template %s of content library %s is deployed, not cloned: its VMs can't be linked clones. Ask a catalog admin to check this item.",
			plan.Template, plan.Library))
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
	// {bindings.<key>}: a binding of the component, CEL giving text.
	for _, f := range spec.Template.Fields() {
		key, ok := strings.CutPrefix(f, "bindings.")
		if !ok {
			continue
		}
		if v, err := policy.Eval(c.Bindings[key], ev.Blueprint.Vars(ev.Inputs)); err == nil {
			vals[f], _ = v.(string)
		}
		if strings.TrimSpace(vals[f]) == "" && strict {
			add(bindingField(c.Bindings[key], ""), "invalid",
				fmt.Sprintf("The VMs' names read bindings.%s, which gives no text from these inputs. Ask a catalog admin to check this item.", key))
		}
	}
	prefix, err := spec.Template.Prefix(vals)
	if err != nil {
		return part, nil // inputs used by the name are still missing; the schema reports them
	}
	part.prefix, part.counter = prefix, spec.counterKey(prefix)
	zone := part.zone()
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
		if p.linkedClone {
			c.LinkedClone = present.Ptr(true)
		}
		if deps := ev.Blueprint.DependsOn(p.comp); len(deps) > 0 {
			c.DependsOn = &deps
		}
		// Its NSX groups: the deployment's by their titles, existing ones by
		// their names.
		var groups []string
		for _, g := range p.vm.Groups {
			if id, ok := blueprint.ExistingGroup(g); ok {
				groups = append(groups, cmp.Or(ev.nsxGroups[id].Name, id))
				continue
			}
			for _, sg := range ev.Blueprint.SecurityGroups {
				if sg.Key == g {
					groups = append(groups, cmp.Or(sg.Title, sg.Key)+" (new)")
				}
			}
		}
		if len(groups) > 0 {
			c.NsxGroups = &groups
		}
		if p.guest.OSFamily == "linux" || p.guest.OSFamily == "windows" {
			c.OsFamily = present.Ptr(api.PreviewComponentOsFamily(p.guest.OSFamily))
		}
		c.Placement = &api.ComponentPlacement{Cluster: str(p.vm.Cluster), Datastore: str(p.vm.Datastore),
			StoragePolicy: str(p.vm.StoragePolicy), Vcenter: str(p.vcenter), ResourcePool: str(p.vm.ResourcePool),
			DatastoreCluster: str(p.pod)}
		if len(p.hosts) > 0 {
			c.Placement.Hosts = present.Ptr(slices.Clone(p.hosts))
		}
		if p.strategy != "" {
			c.Placement.Strategy = present.Ptr(api.PlacementStrategy(p.strategy))
		}
		if len(p.constraints) > 0 {
			c.Placement.Constraints = present.Ptr(presentConstraints(p.constraints))
		}
		if p.image != nil {
			c.Placement.Image = &api.PlacementImage{Key: p.image.Key, Label: p.image.Label, Excluded: nonNilStrings(p.imageExcluded)}
		}
		if p.chosen {
			c.Placement.Chosen = present.Ptr(true)
		}
		if p.rule != nil {
			c.Placement.Folder = str(blueprint.FolderUnder(p.rule.Folder, p.vm.Folder))
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
		if p.vm.DHCP {
			first.Ipam = api.PreviewComponentNicsIpam(blueprint.IPAMDHCP)
		}
		if p.subnet != nil {
			first.Subnet = str(p.subnet.Subnet)
			if p.vm.IPv6 {
				first.Subnet = str(p.subnet.Subnet + " and " + p.subnet.Subnet6)
			}
		}
		if p.segment != "" {
			first.Network = "A new NSX segment (" + p.segment + ")"
			if sg := p.segments; sg != nil {
				first.Network += " on " + sg.Tier1
				first.Subnet = str(fmt.Sprintf("/%d from %s, when the request runs", sg.PrefixLength, sg.IPBlock))
			}
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
			if i < len(p.nicSegments) && p.nicSegments[i] != "" {
				x.Network = "A new NSX segment (" + p.nicSegments[i] + ")"
				if sg := p.segments; sg != nil {
					x.Network += " on " + sg.Tier1
					x.Subnet = str(fmt.Sprintf("/%d from %s, when the request runs", sg.PrefixLength, sg.IPBlock))
				}
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
				return withSuffix(name, "")
			}
		}
	}
	return withSuffix(ev.Item.Name, number)
}

// vmUnit is the plan for one VM of a component.
func vmUnit(ev *Evaluation, p vmPart, deploymentID, name string, index int) provision.VMUnit {
	vm := provision.VMUnit{
		DeploymentID: deploymentID, Component: p.comp.Key, Index: index, Name: name,
		VCPU: p.vm.VCPU, MemoryGB: p.vm.MemoryGB, DiskGB: p.vm.DiskGB,
		Cluster: p.vm.Cluster, Datastore: p.vm.Datastore, Network: p.vm.Network, Template: p.vm.Template,
		Library: p.vm.Library, StoragePolicy: p.vm.StoragePolicy, VCenter: p.vcenter, Provisioning: p.vm.Provisioning,
		LinkedClone: p.linkedClone,
	}
	if p.rule != nil {
		vm.Folder = blueprint.FolderUnder(p.rule.Folder, p.vm.Folder)
	}
	if p.image != nil {
		vm.ImageUserData = p.image.UserData
	}
	vm.CloudbaseInit = p.cloned != nil && p.cloned.CloudbaseInit && p.guest.OSFamily == "windows"
	vm.PreventDelete = p.vm.PreventDelete
	if len(ev.Project.Properties)+len(p.vm.Properties) > 0 {
		vm.Properties = map[string]string{}
		maps.Copy(vm.Properties, ev.Project.Properties)
		maps.Copy(vm.Properties, p.vm.Properties)
	}
	vm.ResourcePool, vm.Hosts = p.vm.ResourcePool, p.hosts
	if p.sdrs {
		vm.DatastoreCluster, vm.PodDatastores = p.pod, p.podDatastores
	}
	// On a segment made for the deployment, its subnet is carved when the
	// request runs (ADR-0028).
	if p.segment != "" {
		vm.Network, vm.Segment = provision.SegmentName(deploymentID, p.segment), p.segment
		if sg := p.segments; sg != nil {
			vm.FQDN, vm.DNSZone, vm.DNSServers = name+"."+sg.DNSZone, sg.DNSZone, sg.DNSServers
		}
	}
	// The NSX groups it joins: its deployment's, by their tag, and existing
	// ones, by the tags they select VMs by (ADR-0028).
	for _, g := range p.vm.Groups {
		if id, ok := blueprint.ExistingGroup(g); ok {
			if eg, ok := ev.nsxGroups[id]; ok {
				vm.NSXGroups = append(vm.NSXGroups, eg)
			}
			continue
		}
		id := provision.GroupName(deploymentID, g)
		vm.NSXGroups = append(vm.NSXGroups, provision.VMGroup{ID: id, Name: id, Key: g, Tags: []nsx.Tag{provision.GroupTag(id)}})
	}
	// Those a day-2 action may put it in: its deployment's, and the
	// existing ones its item's VMs join.
	if len(vm.NSXGroups) > 0 {
		for _, g := range ev.Blueprint.SecurityGroups {
			id := provision.GroupName(deploymentID, g.Key)
			vm.NSXOffered = append(vm.NSXOffered, provision.VMGroup{ID: id, Name: id, Key: g.Key, Tags: []nsx.Tag{provision.GroupTag(id)}})
		}
		for _, id := range ev.joinable {
			if eg, ok := ev.nsxGroups[id]; ok {
				vm.NSXOffered = append(vm.NSXOffered, eg)
			}
		}
	}
	// On DHCP, named in the guest's domain, without DNS records rvav makes.
	if vm.DHCP = p.vm.DHCP; vm.DHCP {
		vm.FQDN = strings.TrimSuffix(name+"."+p.guest.Domain, ".")
	}
	if sn := p.subnet; sn != nil {
		vm.FQDN = name + "." + sn.DNSZone
		vm.Subnet, vm.SubnetID, vm.Gateway = sn.Subnet, sn.EfficientIPSubnetID, sn.Gateway
		vm.PrefixLength, vm.DNSZone, vm.DNSServers = prefixLength(sn.Subnet), sn.DNSZone, sn.DNSServers
		if p.vm.IPv6 {
			vm.Subnet6, vm.Subnet6ID, vm.Gateway6, vm.PrefixLength6 = sn.Subnet6, sn.EfficientIPSubnet6ID, sn.Gateway6, prefixLength(sn.Subnet6)
		}
	}
	for _, d := range p.vm.Disks {
		d.Datastore = cmp.Or(d.Datastore, p.vm.Datastore)
		vm.Disks = append(vm.Disks, d)
	}
	for i, n := range p.vm.NICs {
		nu := provision.NICUnit{Key: n.Key, Network: n.Network, IPAM: n.IPAM}
		// On a segment made for the deployment: its subnet once carved.
		if i < len(p.nicSegments) && p.nicSegments[i] != "" {
			nu.Network, nu.Segment = provision.SegmentName(deploymentID, p.nicSegments[i]), p.nicSegments[i]
			if sg := p.segments; sg != nil {
				nu.FQDN = name + "-" + n.Key + "." + sg.DNSZone
				nu.DNSZone, nu.DNSServers = sg.DNSZone, sg.DNSServers
			}
		}
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
	// Worked out as it is cloned, over the inputs it reads (a secret one
	// sealed) (ADR-0024).
	if expr := p.comp.Bindings["tags"]; expr != "" && p.comp.Refs().BuildTags {
		vm.TagsBinding, vm.TagsInputs = expr, map[string]any{}
		rd, _ := policy.ReadsOf(expr)
		secret := present.SecretFields(ev.Version.UISchema)
		for k, v := range ev.Inputs {
			if rd.AllInputs || slices.Contains(rd.Inputs, k) {
				if secret[k] && !secretinput.IsMarker(v) {
					v = secretinput.Marker(k)
				}
				vm.TagsInputs[k] = v
			}
		}
	}
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

// componentUnit is a component's plan: its guest, anti-affinity and jobs,
// the components it is built after and those it reads.
func componentUnit(ev *Evaluation, p vmPart, order []string) (provision.ComponentUnit, error) {
	jobs, err := postConfigJobs(ev, p.vm, order)
	return provision.ComponentUnit{Key: p.comp.Key, Title: p.title(), Guest: p.guest,
		AntiAffinity: p.comp.AntiAffinity && p.count > 1, PostConfig: jobs,
		DependsOn: ev.Blueprint.DependsOn(p.comp), Reads: ev.Blueprint.Reads(p.comp)}, err
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
	plan.Segments, plan.Security = planSegments(ev, plan.VMs), planSecurity(ev, plan.VMs)
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
			LeaseRuleID: ev.Lease.RuleID, LeaseCapRuleID: ev.Lease.CapRuleID,
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
		zone := p.zone()
		names := &nameCheck{ctx: ctx, ipam: s.IPAM, vcenter: taken, rvav: ours, zone: zone}
		if p.check != nil {
			names.dns = p.check.dns
		}
		for i := range p.count {
			seq, err := s.Store.NextName(ctx, tx, p.counter, p.naming.Start(), p.naming.Step(), func(n int) bool { return names.taken(p.naming.Name(p.prefix, n)) })
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

// planSegments are the NSX segments made for the deployments of these VMs
// (ADR-0028): one for each segment a deployment's VMs are on.
func planSegments(ev *Evaluation, vms []provision.VMUnit) []provision.SegmentUnit {
	parts := map[string]vmPart{}
	for _, p := range ev.Parts {
		parts[p.comp.Key] = p
	}
	var out []provision.SegmentUnit
	seen := map[string]bool{}
	for _, vm := range vms {
		p, ok := parts[cmp.Or(vm.Component, blueprint.SingleKey)]
		if !ok || p.segments == nil {
			continue
		}
		// Those its first adapter and its added ones are on.
		keys := []string{vm.Segment}
		for _, n := range vm.NICs {
			keys = append(keys, n.Segment)
		}
		for _, key := range keys {
			if key == "" || seen[vm.DeploymentID+"/"+key] {
				continue
			}
			seen[vm.DeploymentID+"/"+key] = true
			sg, name := p.segments, provision.SegmentName(vm.DeploymentID, key)
			out = append(out, provision.SegmentUnit{DeploymentID: vm.DeploymentID, Key: key, ID: name, Name: name, Tier1: sg.Tier1,
				IPBlock: sg.IPBlock, PrefixLength: sg.PrefixLength, DNSZone: sg.DNSZone, DNSServers: sg.DNSServers})
		}
	}
	return out
}

// evaluateNSXGroups reads from NSX what the blueprint's security groups
// refer to (ADR-0028): the existing groups its VMs join, and the tags that
// put a VM in them; the groups and services its rules name.
func (s *Service) evaluateNSXGroups(ctx context.Context, ev *Evaluation) error {
	add := func(code, msg string) { ev.Errors = append(ev.Errors, fieldErr("", code, msg)) }
	b := ev.Blueprint
	existing := map[string]bool{}
	var order []string
	note := func(name string) {
		if id, ok := blueprint.ExistingGroup(name); ok && !existing[id] {
			existing[id] = true
			order = append(order, id)
		}
	}
	services := map[string]bool{}
	ev.joinable = nil
	keys := map[string]bool{}
	for _, g := range b.SecurityGroups {
		keys[g.Key] = true
	}
	// The groups its VMs are in: the blueprint's, and those the form chose.
	for _, p := range ev.Parts {
		for _, g := range slices.Concat(p.comp.Groups, p.vm.Groups) {
			if id, ok := blueprint.ExistingGroup(g); ok && !existing[id] {
				ev.joinable = append(ev.joinable, id)
			} else if !ok && !keys[g] {
				ev.Errors = append(ev.Errors, fieldErr(bindingField(p.comp.Bindings["groups"], ""), "unknown_group",
					fmt.Sprintf("%q is none of the item's security groups, nor an NSX group as nsx:<ID>.", g)))
			}
			note(g)
		}
	}
	for _, g := range b.SecurityGroups {
		for _, r := range g.Rules {
			for _, f := range r.From {
				note(f)
			}
			for _, sv := range r.Services {
				if sv != "any" {
					services[sv] = true
				}
			}
		}
	}
	ev.nsxGroups = map[string]provision.VMGroup{}
	if len(b.SecurityGroups) == 0 && len(order) == 0 {
		return nil
	}
	if s.Config.NSX.URL == "" {
		add("no_nsx", "This item puts its VMs in NSX security groups, and rvav has no NSX Manager. Ask an administrator to set one in Integrations.")
		return nil
	}
	client, err := s.nsx()
	if err != nil {
		return err
	}
	for _, id := range order {
		g, err := client.Group(ctx, id)
		switch {
		case errors.Is(err, nsx.ErrNotFound):
			add("unknown_group", fmt.Sprintf("NSX has no group %s. Ask a catalog admin to check this item.", id))
			continue
		case err != nil:
			add("nsx_unavailable", "NSX couldn't be read: "+err.Error())
			return nil
		case g.RvavMade():
			add("rvav_group", fmt.Sprintf("NSX group %s is one rvav made for a deployment: no item may name it.", cmp.Or(g.DisplayName, id)))
			continue
		}
		vg := provision.VMGroup{ID: id, Name: cmp.Or(g.DisplayName, id)}
		// Those VMs join select them by tag; a rule's source needn't.
		if slices.Contains(ev.joinable, id) {
			tags, ok := g.JoinTags()
			if !ok {
				add("not_by_tag", fmt.Sprintf("NSX group %s doesn't select VMs by tag: rvav can't put VMs in it.", vg.Name))
				continue
			}
			vg.Tags = tags
		}
		ev.nsxGroups[id] = vg
	}
	if len(services) > 0 {
		known, err := client.Services(ctx)
		if err != nil {
			add("nsx_unavailable", "NSX couldn't be read: "+err.Error())
			return nil
		}
		for _, k := range known {
			delete(services, k.ID)
		}
		for _, sv := range slices.Sorted(maps.Keys(services)) {
			add("unknown_service", fmt.Sprintf("NSX has no service %s. Ask a catalog admin to check this item.", sv))
		}
	}
	return nil
}

// planSecurity is what NSX makes for the security groups of the
// deployments of these VMs (ADR-0028): the blueprint's groups for each,
// and the firewall rules into them, in a security policy of their own.
func planSecurity(ev *Evaluation, vms []provision.VMUnit) []provision.SecurityUnit {
	b := ev.Blueprint
	if len(b.SecurityGroups) == 0 {
		return nil
	}
	var out []provision.SecurityUnit
	seen := map[string]bool{}
	for _, vm := range vms {
		dep := vm.DeploymentID
		if seen[dep] {
			continue
		}
		seen[dep] = true
		path := func(name string) string {
			if id, ok := blueprint.ExistingGroup(name); ok {
				return nsx.GroupPath + id
			}
			if name == "any" {
				return "ANY"
			}
			return nsx.GroupPath + provision.GroupName(dep, name)
		}
		// ANY with others is ANY.
		anyOf := func(list []string) []string {
			if slices.Contains(list, "ANY") {
				return []string{"ANY"}
			}
			return list
		}
		u := provision.SecurityUnit{DeploymentID: dep, Policy: provision.PolicyName(dep)}
		seq := 0
		for _, g := range b.SecurityGroups {
			id := provision.GroupName(dep, g.Key)
			u.Groups = append(u.Groups, provision.GroupUnit{Key: g.Key, ID: id, Title: g.Title, Tag: provision.GroupTag(id)})
			for j, r := range g.Rules {
				seq += 10
				rule := nsx.Rule{ID: fmt.Sprintf("%s-%d", g.Key, j+1), DisplayName: "Into " + cmp.Or(g.Title, g.Key),
					DestinationGroups: []string{nsx.GroupPath + id}, Scope: []string{nsx.GroupPath + id},
					Action: strings.ToUpper(cmp.Or(r.Action, "allow")), SequenceNumber: seq}
				for _, f := range r.From {
					rule.SourceGroups = append(rule.SourceGroups, path(f))
				}
				for _, sv := range r.Services {
					if sv == "any" {
						sv = "ANY"
					} else {
						sv = nsx.ServicePath + sv
					}
					rule.Services = append(rule.Services, sv)
				}
				rule.SourceGroups, rule.Services = anyOf(rule.SourceGroups), anyOf(rule.Services)
				u.Rules = append(u.Rules, rule)
			}
		}
		out = append(out, u)
	}
	return out
}
