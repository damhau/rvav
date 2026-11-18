package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Constraint tags (ADR-0035): among the clusters, datastores and networks
// its placement rules allow, a component's VMs go to those whose vSphere
// tags meet its constraints. A hard constraint rules out what doesn't meet
// it, failing when nothing does; a soft one prefers what does, and is let
// go when nothing does.

// constraintResult is what one constraint did.
type constraintResult struct {
	blueprint.Constraint
	// object is what the VMs (or the disk) were placed on, and met whether
	// it meets the constraint.
	object string
	met    bool
	// excluded are the allowed objects it ruled out, or passed over.
	excluded []string
}

// constraintSet is a component's constraints, worked out.
type constraintSet []blueprint.Constraint

// of is the constraints of a kind, the VMs' (disk 0) or a disk's own.
func (s constraintSet) of(kind string, disk int) []blueprint.Constraint {
	var out []blueprint.Constraint
	for _, c := range s {
		if c.Kind == kind && c.Disk == disk {
			out = append(out, c)
		}
	}
	return out
}

func hard(list []blueprint.Constraint) []blueprint.Constraint {
	return slices.DeleteFunc(slices.Clone(list), func(c blueprint.Constraint) bool { return c.Soft })
}

func soft(list []blueprint.Constraint) []blueprint.Constraint {
	return slices.DeleteFunc(slices.Clone(list), func(c blueprint.Constraint) bool { return !c.Soft })
}

// constraintVars are what a constraint's CEL reads: the bindings' inputs,
// sizes and segments, the environment's key and the project.
func constraintVars(ev *Evaluation) policy.Vars {
	vars := ev.Blueprint.Vars(ev.Inputs)
	vars["environment"] = ev.Env.Key
	vars["project"] = map[string]any{"id": ev.Project.ID, "code": ev.Project.Code, "name": ev.Project.Name}
	return vars
}

// constraintsOf works out a component's constraints; one that can't be is
// an error at submit, and left out of a preview while inputs are missing.
func constraintsOf(ev *Evaluation, c blueprint.Component, strict bool, add func(field, code, msg string)) constraintSet {
	list, problems := c.EvaluateConstraints(constraintVars(ev))
	for _, p := range problems {
		switch {
		case !p.Missing:
			add("", "invalid", fmt.Sprintf("Constraint %s gives %s. Ask a catalog admin to check this item.", p.At, p.Msg))
		case strict:
			add("", "invalid", fmt.Sprintf("Constraint %s could not be worked out from the inputs. Ask a catalog admin to check this item.", p.At))
		}
	}
	return list
}

// narrow keeps, of the candidates, those meeting each constraint in turn.
// A hard constraint nothing meets is returned as failed.
func narrow[T any](cands []T, cs []blueprint.Constraint, tags func(T) []string, name func(T) string) ([]T, []constraintResult, *blueprint.Constraint) {
	var results []constraintResult
	for _, c := range cs {
		var in []T
		var out []string
		for _, x := range cands {
			if c.Meets(tags(x)) {
				in = append(in, x)
			} else {
				out = append(out, name(x))
			}
		}
		if len(in) == 0 {
			results = append(results, constraintResult{Constraint: c})
			if !c.Soft {
				return nil, results, &c
			}
			continue // let go
		}
		results = append(results, constraintResult{Constraint: c, excluded: out})
		cands = in
	}
	return cands, results, nil
}

// placedOn fills in what the results' constraints were placed on.
func placedOn(results []constraintResult, name string, tags []string) []constraintResult {
	for i := range results {
		results[i].object, results[i].met = name, results[i].Meets(tags)
	}
	return results
}

// needs says what a constraint asks for: "a cluster tagged rvav-tier/gold",
// "a datastore not tagged rvav-tier/bronze".
func needs(c blueprint.Constraint, what string) string {
	if c.Not {
		return fmt.Sprintf("%s not tagged %s", what, c.Tag)
	}
	return fmt.Sprintf("%s tagged %s", what, c.Tag)
}

// whose is who needs a constraint met: the item, or one of its added disks.
func whose(c blueprint.Constraint) string {
	if c.Disk > 0 {
		return fmt.Sprintf("Disk %d", c.Disk+1)
	}
	return "This item"
}

func clusterName(c *store.InvCluster) string       { return c.Name }
func clusterTags(c *store.InvCluster) []string     { return c.Tags }
func datastoreName(d store.InvDatastore) string    { return d.Name }
func datastoreTags(d store.InvDatastore) []string  { return d.Tags }
func networkName(n store.InvNetwork) string        { return n.Name }
func networkTags(n store.InvNetwork) []string      { return n.Tags }
func hasAny[T any](list []T, ok func(T) bool) bool { return slices.ContainsFunc(list, ok) }
func meetsAll(cs []blueprint.Constraint, tags []string) bool {
	return !slices.ContainsFunc(cs, func(c blueprint.Constraint) bool { return !c.Meets(tags) })
}

// placementQuery is what choosing a cluster for a component needs to know.
type placementQuery struct {
	plan  blueprint.VMPlan
	count int
	apart bool
	// anyDatastore: rvav chooses the VMs' datastore (else plan.Datastore);
	// anyNetwork: it chooses their network by its constraints.
	anyDatastore, anyNetwork bool
	hasTemplate              func(vcenter string) bool
	cons                     constraintSet
	// disks are the added disks whose datastores rvav chooses by their own
	// constraints, by 1 + index; vmGB is what the VMs' datastore holds.
	disks map[int]int
	vmGB  int
}

// choose is the cluster a component's VMs go to when the blueprint doesn't
// say: among those allowed in a vCenter with the template, meeting its
// compute constraints, with its network (or one meeting its network
// constraints), enough hosts to keep the VMs apart, and a datastore with
// room meeting its storage constraints (the one given, else any allowed),
// the one with the most free memory, after its soft compute constraints.
// It says why when it finds none because of a hard constraint.
func (pl *placement) choose(q placementQuery) (*store.InvCluster, []constraintResult, string) {
	var cands []*store.InvCluster
	for i := range pl.clusters {
		c := &pl.clusters[i]
		// A name in two vCenters is never chosen (its rule must say which),
		// and a name twice in one is the first.
		if pl.ambiguous(c.Name) || pl.cluster(c.Name) != c || !q.hasTemplate(c.VCenter) {
			continue
		}
		cands = append(cands, c)
	}
	compute := q.cons.of(blueprint.Compute, 0)
	cands, results, failed := narrow(cands, hard(compute), clusterTags, clusterName)
	if failed != nil {
		return nil, results, fmt.Sprintf("%s needs %s, and none allowed here is.", whose(*failed), needs(*failed, "a cluster"))
	}
	// Hard storage and network constraints: a cluster with somewhere to
	// meet them.
	storage := map[int][]blueprint.Constraint{}
	if q.anyDatastore {
		storage[0] = hard(q.cons.of(blueprint.Storage, 0))
	}
	for d := range q.disks {
		storage[d] = hard(q.cons.of(blueprint.Storage, d))
	}
	for _, d := range slices.Sorted(maps.Keys(storage)) {
		for i, c := range storage[d] {
			so := storage[d][:i+1]
			cands = slices.DeleteFunc(cands, func(cl *store.InvCluster) bool {
				return !hasAny(pl.datastoresFor(cl.Name), func(x store.InvDatastore) bool { return meetsAll(so, x.Tags) })
			})
			if len(cands) == 0 {
				return nil, results, fmt.Sprintf("%s needs %s, and no cluster allowed here has one.", whose(c), needs(c, "a datastore"))
			}
		}
	}
	if q.anyNetwork {
		network := hard(q.cons.of(blueprint.Network, 0))
		for i, c := range network {
			so := network[:i+1]
			cands = slices.DeleteFunc(cands, func(cl *store.InvCluster) bool {
				return !hasAny(pl.networksFor(cl.Name), func(n store.InvNetwork) bool { return pl.hasSubnet(n.Name) && meetsAll(so, n.Tags) })
			})
			if len(cands) == 0 {
				return nil, results, fmt.Sprintf("%s needs %s with a subnet, and no cluster allowed here has one.", whose(c), needs(c, "a network"))
			}
		}
	}
	cands = slices.DeleteFunc(cands, func(c *store.InvCluster) bool { return !pl.fits(c, q) })
	cands, softs, _ := narrow(cands, soft(compute), clusterTags, clusterName)
	results = append(results, softs...)
	var best *store.InvCluster
	for _, c := range cands {
		if best == nil || c.MemoryTotalMB-c.MemoryUsedMB > best.MemoryTotalMB-best.MemoryUsedMB {
			best = c
		}
	}
	if best == nil {
		return nil, results, ""
	}
	return best, placedOn(results, best.Name, best.Tags), ""
}

// fits says whether a cluster has the component's network, hosts to keep
// its VMs apart, and room on datastores meeting its hard storage
// constraints.
func (pl *placement) fits(c *store.InvCluster, q placementQuery) bool {
	if q.plan.Network != "" && !slices.ContainsFunc(pl.networksFor(c.Name), func(n store.InvNetwork) bool { return n.Name == q.plan.Network }) {
		return false
	}
	if q.apart && q.count > 1 && c.HostCount > 0 && c.HostCount < q.count {
		return false
	}
	room := func(disk, gb int) bool {
		cs := hard(q.cons.of(blueprint.Storage, disk))
		return hasAny(pl.datastoresFor(c.Name), func(d store.InvDatastore) bool {
			if disk == 0 && !q.anyDatastore {
				return d.Name == q.plan.Datastore
			}
			return d.FreeGB >= int64(gb*q.count) && meetsAll(cs, d.Tags)
		})
	}
	if !room(0, q.vmGB) {
		return false
	}
	for d, gb := range q.disks {
		if !room(d, gb) {
			return false
		}
	}
	return true
}

func (pl *placement) hasSubnet(network string) bool {
	_, ok := pl.subnets[network]
	return ok
}

// pickDatastore is the allowed datastore of the cluster with room for need
// GB meeting the constraints, the roomiest of those meeting the soft ones
// too when any does. It says why when there is none because of a hard
// constraint ("" for no room).
func (pl *placement) pickDatastore(cluster string, need int, cs []blueprint.Constraint) (string, []constraintResult, string) {
	cands := slices.DeleteFunc(slices.Clone(pl.datastoresFor(cluster)), func(d store.InvDatastore) bool { return d.FreeGB < int64(need) })
	if len(cands) == 0 {
		return "", nil, ""
	}
	cands, results, failed := narrow(cands, append(hard(cs), soft(cs)...), datastoreTags, datastoreName)
	if failed != nil {
		return "", results, fmt.Sprintf("%s needs %s on %s with room for %d GB, and none allowed is.", whose(*failed), needs(*failed, "a datastore"), cluster, need)
	}
	best := cands[0]
	for _, d := range cands[1:] {
		if d.FreeGB > best.FreeGB {
			best = d
		}
	}
	return best.Name, placedOn(results, best.Name, best.Tags), ""
}

// pickNetwork is the first allowed network of the cluster, with a subnet,
// meeting the constraints.
func (pl *placement) pickNetwork(cluster string, cs []blueprint.Constraint) (string, []constraintResult, string) {
	cands := slices.DeleteFunc(slices.Clone(pl.networksFor(cluster)), func(n store.InvNetwork) bool { return !pl.hasSubnet(n.Name) })
	cands, results, failed := narrow(cands, append(hard(cs), soft(cs)...), networkTags, networkName)
	if failed != nil {
		return "", results, fmt.Sprintf("%s needs %s on %s with a subnet, and none allowed is.", whose(*failed), needs(*failed, "a network"), cluster)
	}
	if len(cands) == 0 {
		return "", results, fmt.Sprintf("No network allowed on %s has a subnet. Ask an administrator.", cluster)
	}
	return cands[0].Name, placedOn(results, cands[0].Name, cands[0].Tags), ""
}

// checkGiven checks constraints on what the blueprint or the form gave: a hard
// one it doesn't meet is an error.
func checkGiven(cs []blueprint.Constraint, what, name string, tags []string, add func(code, msg string)) []constraintResult {
	var results []constraintResult
	for _, c := range cs {
		r := constraintResult{Constraint: c, object: name, met: c.Meets(tags)}
		results = append(results, r)
		switch {
		case r.met || c.Soft:
		case c.Not:
			add("constraint", fmt.Sprintf("%s %s is tagged %s, which %s rules out.", what, name, c.Tag, strings.ToLower(whose(c))))
		default:
			add("constraint", fmt.Sprintf("%s %s isn't tagged %s, which %s needs.", what, name, c.Tag, strings.ToLower(whose(c))))
		}
	}
	return results
}

// placementNote says how a component's constraints placed it, for its
// request's timeline: those met, and the soft ones let go.
func placementNote(label string, results []constraintResult) string {
	var met []string
	var notes []string
	for _, r := range results {
		switch {
		case r.met:
			met = append(met, r.String())
		case r.Soft && r.object != "":
			notes = append(notes, fmt.Sprintf("soft constraint %s not met: placed on %s without it", strings.TrimSuffix(r.String(), ":soft"), r.object))
		}
	}
	if len(met) > 0 {
		notes = append([]string{"placed by " + strings.Join(met, ", ")}, notes...)
	}
	note := strings.Join(notes, "; ")
	switch {
	case note == "":
		return ""
	case label == "":
		return strings.ToUpper(note[:1]) + note[1:]
	}
	return label + note
}

// placementNotes says how constraint tags placed a request's VMs, each
// component's after its title, and a composite's under its step's.
func (ev *Evaluation) placementNotes() []string {
	var out []string
	for _, p := range ev.Parts {
		if n := placementNote(p.label, p.constraints); n != "" {
			out = append(out, n)
		}
	}
	for _, c := range ev.Composite {
		if c.sub == nil {
			continue
		}
		for _, n := range c.sub.placementNotes() {
			// A component titled as its step says so once.
			if !strings.HasPrefix(n, c.title+": ") {
				n = c.title + ": " + n
			}
			out = append(out, n)
		}
	}
	return out
}

func presentConstraints(results []constraintResult) []api.PlacementConstraint {
	out := make([]api.PlacementConstraint, len(results))
	for i, r := range results {
		out[i] = api.PlacementConstraint{Kind: api.PlacementConstraintKind(r.Kind), Tag: r.Tag, Negated: r.Not, Soft: r.Soft,
			Met: r.met, Excluded: nonNilStrings(r.excluded)}
		if r.object != "" {
			out[i].Object = &r.object
		}
		if r.Disk > 0 {
			out[i].Disk = present.Ptr(r.Disk)
		}
	}
	return out
}

// checkConstraints checks a blueprint's or a component's constraints as
// written: a constraint, or CEL giving some (ADR-0035).
func checkConstraints(ptr string, k *blueprint.Constraints) []api.FieldError {
	var errs []api.FieldError
	for _, e := range k.Entries() {
		errs = append(errs, checkConstraint(ptr+"/"+e.At, e.Text)...)
	}
	return errs
}

func checkConstraint(at, entry string) []api.FieldError {
	if !blueprint.HasExpr(entry) {
		if _, err := blueprint.ParseConstraint(entry); err != nil {
			return []api.FieldError{fieldErr(at, "pattern", "Write [!]category/tag[:soft], as rvav-tier/gold or !rvav-zone/dmz: "+err.Error()+".")}
		}
		return nil
	}
	exprs, err := blueprint.ExprsOf(entry)
	if err != nil {
		return []api.FieldError{fieldErr(at, "invalid", "This constraint's ${...} is not right: "+err.Error()+".")}
	}
	for _, x := range exprs {
		if err := policy.Check(x); err != nil {
			return []api.FieldError{fieldErr(at, "invalid", "This constraint's ${...} is not valid CEL: "+err.Error()+
				" (worked out at submit, it reads inputs, sizes, environment and project).")}
		}
	}
	return nil
}

// constraintWarnings says which constraints of a VM blueprint name a tag
// no cluster, datastore or network of the inventory carries, as each kind
// asks: it may be a typo, or a tag not made yet. A constraint holding CEL
// is worked out per request, and a kind the inventory has nothing of isn't
// checked.
func (s *Service) constraintWarnings(ctx context.Context, def api.CatalogVersionDefinition, ptr string) ([]api.FieldError, error) {
	raw, _ := json.Marshal(def.Blueprint)
	b, err := blueprint.Parse(raw)
	if err != nil || b.Kind != blueprint.KindVM {
		return nil, nil
	}
	type written struct{ at, kind, text string }
	var all []written
	walk := func(at string, k *blueprint.Constraints, disks []blueprint.Disk) {
		for _, e := range k.Entries() {
			all = append(all, written{at + "/" + e.At, e.Kind, e.Text})
		}
		for i, d := range disks {
			for j, e := range d.Constraints {
				all = append(all, written{fmt.Sprintf("%s/disks/%d/constraints/%d", at, i, j), blueprint.Storage, e})
			}
		}
	}
	walk(ptr, b.Constraints, b.Disks)
	for i, c := range b.Components {
		walk(fmt.Sprintf("%s/components/%d", ptr, i), c.Constraints, c.Disks)
	}
	if len(all) == 0 {
		return nil, nil
	}
	tags := map[string]map[string]bool{blueprint.Compute: {}, blueprint.Storage: {}, blueprint.Network: {}}
	seen := map[string]bool{}
	clusters, err := s.Store.InventoryClusters(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range clusters {
		seen[blueprint.Compute] = true
		for _, t := range c.Tags {
			tags[blueprint.Compute][t] = true
		}
	}
	datastores, err := s.Store.InventoryDatastores(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range datastores {
		seen[blueprint.Storage] = true
		for _, t := range d.Tags {
			tags[blueprint.Storage][t] = true
		}
	}
	networks, err := s.Store.InventoryNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range networks {
		seen[blueprint.Network] = true
		for _, t := range n.Tags {
			tags[blueprint.Network][t] = true
		}
	}
	what := map[string]string{blueprint.Compute: "cluster", blueprint.Storage: "datastore", blueprint.Network: "network"}
	var out []api.FieldError
	for _, w := range all {
		if blueprint.HasExpr(w.text) || !seen[w.kind] {
			continue
		}
		c, err := blueprint.ParseConstraint(w.text)
		if err != nil || tags[w.kind][c.Tag] {
			continue
		}
		msg := fmt.Sprintf("No %s in vCenter is tagged %s yet: requests fail until one is.", what[w.kind], c.Tag)
		if c.Not || c.Soft {
			msg = fmt.Sprintf("No %s in vCenter is tagged %s: check the tag's name.", what[w.kind], c.Tag)
		}
		out = append(out, fieldErr(w.at, "unknown_tag", msg))
	}
	return out, nil
}
