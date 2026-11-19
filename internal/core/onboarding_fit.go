package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Where adopted VMs go (X1.5): new deployments of an item, or a deployment
// rvav has, as VMs of one of its blueprint's components. They must fit
// it: the component's count, raised in the inputs when an input sets it;
// its OS family; names the deployment's other VMs don't have.

// destination is where adopted VMs go, checked.
type destination struct {
	project store.Project
	env     string
	ownerID string
	itemID  string
	item    string // its slug, for the audit
	label   string // its name, for messages
	version store.ItemVersion
	bp      blueprint.Blueprint
	comp    blueprint.Component
	// compKey is recorded on the VMs: "" for a blueprint without components.
	compKey string
	lease   store.LeasePolicy
	days    *int
	// name: all in one deployment of this name; "" each its own.
	name   string
	inputs map[string]any
	// existing is the deployment they join, when it exists.
	existing *store.DeploymentRow
}

func (d *destination) expires() (*time.Time, *int) {
	if d.days == nil {
		return nil, nil
	}
	t := time.Now().UTC().Add(time.Duration(*d.days) * 24 * time.Hour).Truncate(time.Second)
	return &t, &d.lease.MaxExtensions
}

// newTarget is what new deployments of adopted VMs are.
type newTarget struct {
	projectID, environment, ownerID, itemID string
	leaseDays                               *int
	deploymentName, component               string
	inputs                                  map[string]any
}

type addFunc func(at, code, msg string)

// newDestination checks new deployments' project, environment, owner,
// item, lease, name, component and inputs; what is wrong goes to add,
// under at.
func (s *Service) newDestination(ctx context.Context, userID string, t newTarget, at string, add addFunc) (*destination, error) {
	d := &destination{env: t.environment, ownerID: t.ownerID, name: t.deploymentName}
	project, err := s.Store.GetProject(ctx, t.projectID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		add(at+"/project_id", "not_found", "No such project.")
	case err != nil:
		return nil, err
	case project.ArchivedAt != nil:
		add(at+"/project_id", "archived", project.Name+" is archived: it takes nothing new.")
	}
	d.project = project
	if project.ID != "" {
		if !slices.Contains(project.Environments, t.environment) {
			add(at+"/environment", "invalid", fmt.Sprintf("%s has no environment %s.", project.Name, t.environment))
		} else if d.lease, err = s.Store.LeasePolicy(ctx, project.ID, t.environment); err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		owners, err := s.requesters(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(owners, t.ownerID) {
			add(at+"/owner_id", "invalid", "Choose a member of "+project.Name+" who requests there: owners run the actions on their deployments.")
		}
	}
	item, version, msg := s.adoptionItem(ctx, userID, t.itemID, project)
	if msg != "" {
		add(at+"/catalog_item_id", "invalid", msg)
	}
	d.itemID, d.item, d.label, d.version = item.ID, item.Slug, item.Name, version
	d.days = d.lease.DefaultDays
	if t.leaseDays != nil {
		d.days = t.leaseDays
		switch {
		case *d.days < 1:
			add(at+"/lease_days", "too_short", "A lease is at least one day.")
		case d.lease.MaxDays != nil && *d.days > *d.lease.MaxDays:
			add(at+"/lease_days", "too_long", fmt.Sprintf("Leases in %s can be at most %d days.", t.environment, *d.lease.MaxDays))
		}
	}
	if d.name != "" && !deploymentNamePattern.MatchString(d.name) {
		add(at+"/deployment_name", "pattern", "Lower-case letters, digits, dots and dashes, up to 63, for example web-prd-erp.")
	}
	if msg != "" {
		return d, nil
	}
	if err := d.setComponent(t.component, at, add); err != nil {
		return nil, err
	}
	d.inputs, err = recordedInputs(version, t.inputs, at, add)
	return d, err
}

// existingDestination checks a deployment adopted VMs join. Whether it
// takes them now (ready, its project not archived) is checked as they
// join.
func (s *Service) existingDestination(ctx context.Context, depID, projectID, component, at string, add addFunc) (*destination, error) {
	dep, err := s.Store.GetDeployment(ctx, depID)
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && dep.Status == "deleted":
		add(at+"/deployment_id", "not_found", "No such deployment.")
		return nil, nil
	case err != nil:
		return nil, err
	case projectID != "" && dep.ProjectID != projectID:
		add(at+"/deployment_id", "invalid", dep.Name+" is a deployment of "+dep.ProjectName+".")
		return nil, nil
	}
	d, err := s.joining(ctx, dep, component, at, add)
	if err == nil && d.bp.Kind != blueprint.KindVM {
		add(at+"/deployment_id", "invalid", dep.Name+" is a deployment of "+dep.ItemName+", not of a VM item: it takes no VMs.")
	}
	return d, err
}

// joining is the destination of VMs joining dep.
func (s *Service) joining(ctx context.Context, dep store.DeploymentRow, component, at string, add addFunc) (*destination, error) {
	version, err := s.Store.GetVersion(ctx, dep.VersionID)
	if err != nil {
		return nil, err
	}
	d := &destination{env: dep.Environment, ownerID: dep.OwnerID, itemID: dep.ItemID, item: dep.ItemSlug, label: dep.ItemName,
		version: version, name: dep.Name, existing: &dep, inputs: map[string]any{}}
	_ = json.Unmarshal(dep.Inputs, &d.inputs)
	if d.project, err = s.Store.GetProject(ctx, dep.ProjectID); err != nil {
		return nil, err
	}
	if b, err := blueprint.Parse(version.Blueprint); err != nil || b.Kind != blueprint.KindVM {
		d.bp = b
		return d, nil
	}
	return d, d.setComponent(component, at, add)
}

// setComponent picks the component of the version's blueprint the VMs are
// VMs of.
func (d *destination) setComponent(key, at string, add addFunc) error {
	bp, err := blueprint.Parse(d.version.Blueprint)
	if err != nil {
		return err
	}
	d.bp = bp
	list := bp.ComponentList()
	if !bp.Multi() {
		if key != "" && key != blueprint.SingleKey {
			add(at+"/component", "invalid", d.label+"'s blueprint has no components: leave the component out.")
		}
		d.comp = list[0]
		return nil
	}
	keys := make([]string, len(list))
	for i, c := range list {
		keys[i] = c.Key
		if c.Key == key || key == "" && len(list) == 1 {
			d.comp, d.compKey = c, c.Key
			return nil
		}
	}
	if key == "" {
		add(at+"/component", "required", "Choose the component of "+d.label+" they are VMs of: "+orList(keys)+".")
	} else {
		add(at+"/component", "invalid", d.label+" has no component "+key+": "+orList(keys)+".")
	}
	return nil
}

func orList(s []string) string {
	if len(s) < 2 {
		return strings.Join(s, "")
	}
	return strings.Join(s[:len(s)-1], ", ") + " or " + s[len(s)-1]
}

// recordedInputs checks inputs recorded on adopted VMs' deployments: the
// form's fields, valid, none secret nor worked out by rvav. A field not
// given isn't required: what rvav doesn't record, an update asks for.
func recordedInputs(v store.ItemVersion, given map[string]any, at string, add addFunc) (map[string]any, error) {
	out := map[string]any{}
	if len(given) == 0 {
		return out, nil
	}
	var schema struct {
		Properties map[string]struct {
			ReadOnly bool `json:"readOnly"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(v.FormSchema, &schema)
	secret := present.SecretFields(v.UISchema)
	bad := false
	for _, k := range slices.Sorted(maps.Keys(given)) {
		f, ok := schema.Properties[k]
		switch {
		case !ok:
			add(at+"/inputs/"+k, "unknown_field", "This form has no field named "+k+".")
		case secret[k]:
			add(at+"/inputs/"+k, "secret", k+" is secret: rvav keeps no secret of a VM it didn't build.")
		case f.ReadOnly:
			add(at+"/inputs/"+k, "read_only", "rvav works "+k+" out: it isn't given.")
		default:
			out[k] = given[k]
			continue
		}
		bad = true
	}
	errs, err := validateInputs(v.ID, v.FormSchema, out)
	if err != nil {
		return nil, err
	}
	for _, e := range errs {
		// What isn't given isn't asked for.
		key, _, _ := strings.Cut(strings.TrimPrefix(e.Field, "/inputs/"), "/")
		if _, ok := out[key]; ok && strings.HasPrefix(e.Field, "/inputs/") {
			add(at+e.Field, deref(e.Code), e.Message)
			bad = true
		}
	}
	if bad {
		return nil, nil
	}
	return out, nil
}

// seat is a VM of a deployment, or one joining it.
type seat struct {
	name, component, guestOS string
}

// fitting is what fit checks.
type fitting struct {
	bp     blueprint.Blueprint
	comp   blueprint.Component
	label  string // the item's name
	schema json.RawMessage
	inputs map[string]any
	have   []seat
	vms    []seat
}

// misfit is why VM vm (an index of fitting.vms) can't join.
type misfit struct {
	vm   int
	code string
	msg  string
}

func (m *misfit) Error() string { return m.msg }

var countInputPattern = regexp.MustCompile(`^\s*inputs\.([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// fit says whether f.vms join their component, and the inputs the
// deployment then has when its count input must rise (nil when they stay
// as they are); or why one can't. Joining VMs take the places after the
// component's own, by name, as an update counts them.
func fit(f fitting) (map[string]any, *misfit) {
	key := f.comp.Key
	what := f.label + "'s VMs"
	if f.bp.Multi() {
		what = cmpTitle(f.comp) + " VMs"
	}
	names := map[string]bool{}
	inComp := 0
	for _, h := range f.have {
		names[h.name] = true
		if orKey(h.component) == key {
			inComp++
		}
	}
	for i, v := range f.vms {
		if fam := f.comp.Guest.OSFamily; fam != "" && knownGuest(v.guestOS) && osFamily(v.guestOS) != fam {
			return nil, &misfit{vm: i, code: "no_fit", msg: fmt.Sprintf("%s runs %s (%s): %s run %s.", v.name, familyName(osFamily(v.guestOS)),
				v.guestOS, what, familyName(fam))}
		}
		if names[v.name] {
			return nil, &misfit{vm: i, code: "no_fit", msg: "The deployment has a VM named " + v.name + " already."}
		}
		names[v.name] = true
	}
	total := inComp + len(f.vms)
	if total > blueprint.MaxVMs {
		return nil, &misfit{vm: max(0, blueprint.MaxVMs-inComp), code: "no_fit",
			msg: fmt.Sprintf("A component has %d VMs at most, and %s has %d.", blueprint.MaxVMs, componentName(f), inComp)}
	}
	if !f.bp.Multi() {
		return nil, nil
	}
	_, count, missing := f.bp.EvaluateComponent(f.comp, f.inputs)
	if !slices.Contains(missing, "count") && total <= count {
		return nil, nil
	}
	if m := countInputPattern.FindStringSubmatch(fmt.Sprint(f.comp.Count)); m != nil {
		return raiseCount(f, m[1], total, inComp)
	}
	if slices.Contains(missing, "count") {
		return nil, &misfit{vm: 0, code: "no_fit", msg: fmt.Sprintf("How many VMs %s has, %v, can't be worked out from the deployment's inputs.",
			componentName(f), f.comp.Count)}
	}
	return nil, &misfit{vm: max(0, count-inComp), code: "no_fit",
		msg: fmt.Sprintf("%s has %s (its count), and has %d already.", componentName(f), vmCount(count), inComp)}
}

// raiseCount sets the input a component's count is to its VMs, when the
// form lets it and no other component's count changes with it.
func raiseCount(f fitting, field string, total, inComp int) (map[string]any, *misfit) {
	var schema struct {
		Properties map[string]struct {
			Type    any      `json:"type"`
			Maximum *float64 `json:"maximum"`
			Minimum *float64 `json:"minimum"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(f.schema, &schema)
	prop, ok := schema.Properties[field]
	if !ok {
		return nil, &misfit{code: "no_fit", msg: fmt.Sprintf("%s's count is inputs.%s, which the form doesn't have.", componentName(f), field)}
	}
	if prop.Maximum != nil && float64(total) > *prop.Maximum {
		return nil, &misfit{vm: max(0, int(*prop.Maximum)-inComp), code: "no_fit",
			msg: fmt.Sprintf("%s has as many VMs as %s says, %v at most, and has %d already.", componentName(f), field, *prop.Maximum, inComp)}
	}
	n := float64(total)
	if prop.Minimum != nil && n < *prop.Minimum {
		n = *prop.Minimum
	}
	out := maps.Clone(f.inputs)
	if out == nil {
		out = map[string]any{}
	}
	out[field] = n
	for _, c := range f.bp.ComponentList() {
		if c.Key == f.comp.Key {
			continue
		}
		_, before, _ := f.bp.EvaluateComponent(c, f.inputs)
		if _, after, _ := f.bp.EvaluateComponent(c, out); after != before {
			return nil, &misfit{code: "no_fit", msg: fmt.Sprintf("Setting %s to %d for %s would change how many VMs %s has too.",
				field, total, componentName(f), cmpTitle(c))}
		}
	}
	if _, got, missing := f.bp.EvaluateComponent(f.comp, out); slices.Contains(missing, "count") || got < total {
		return nil, &misfit{code: "no_fit", msg: fmt.Sprintf("Setting %s to %d doesn't give %s %d VMs.", field, total, componentName(f), total)}
	}
	return out, nil
}

func orKey(component string) string {
	if component == "" {
		return blueprint.SingleKey
	}
	return component
}

func cmpTitle(c blueprint.Component) string {
	if c.Title != "" {
		return c.Title
	}
	return c.Key
}

func componentName(f fitting) string {
	if f.bp.Multi() {
		return cmpTitle(f.comp)
	}
	return "The deployment"
}

func vmCount(n int) string {
	if n == 1 {
		return "1 VM"
	}
	return fmt.Sprintf("%d VMs", n)
}

// knownGuest: vCenter knows the guest's OS, not just "other".
func knownGuest(guestOS string) bool {
	return guestOS != "" && !strings.HasPrefix(strings.ToLower(guestOS), "other")
}

func familyName(f string) string {
	if f == "windows" {
		return "Windows"
	}
	return "Linux"
}
