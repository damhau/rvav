package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// Custom resources in blueprints and composites (ADR-0056).

// usableResourceType is the type a blueprint or a composite step names, or
// why it can't be used in the project: turned off, or its create workflow
// can't run there.
func (s *Service) usableResourceType(ctx context.Context, key, projectID string) (store.ResourceType, string, error) {
	t, err := s.Store.GetResourceType(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return t, "the resource type " + key + " doesn't exist.", nil
	} else if err != nil {
		return t, "", err
	}
	if !t.Enabled {
		return t, t.Title + " is turned off: ask an administrator to turn it on.", nil
	}
	if c := t.Lifecycle.Create; c.Kind == "workflow" {
		if _, why, err := s.usableWorkflow(ctx, c.Name, "resource_type", projectID); err != nil || why != "" {
			return t, why, err
		}
	}
	return t, "", nil
}

func resourceUnit(t store.ResourceType, key, title string, with map[string]any) provision.ResourceUnit {
	return provision.ResourceUnit{Key: key, Title: cmp.Or(title, t.Title), Type: t.Key,
		System: provision.DriverSystem(&t.Lifecycle.Create), With: with}
}

// evaluateResources checks the types of a blueprint's custom resources.
func (s *Service) evaluateResources(ctx context.Context, ev *Evaluation) error {
	ev.Resources = nil
	for _, r := range ev.Blueprint.Resources {
		t, why, err := s.usableResourceType(ctx, r.Type, ev.Project.ID)
		if err != nil {
			return err
		}
		if why != "" {
			ev.Errors = append(ev.Errors, fieldErr("", "not_available", cmp.Or(r.Title, r.Key)+": "+why))
			continue
		}
		ev.Resources = append(ev.Resources, resourceUnit(t, r.Key, r.Title, r.With))
	}
	return nil
}

// planResources are a blueprint's custom resources, made in its deployment.
func (ev *Evaluation) planResources(deploymentID string) []provision.ResourceUnit {
	var out []provision.ResourceUnit
	for _, u := range ev.Resources {
		u.DeploymentID = deploymentID
		out = append(out, u)
	}
	return out
}

// compositeDeployment is the VM step whose deployment a composite's resource
// step joins: the one it names, else the only one before it.
func compositeDeployment(parts []compositePart, name string, group int) (string, string) {
	var vms []string
	for _, p := range parts {
		if p.sub != nil && p.group < group {
			vms = append(vms, p.step.Key)
		}
	}
	switch {
	case name != "" && slices.Contains(vms, name):
		return name, ""
	case name != "":
		return "", "its deployment is " + name + "'s, but no VM step before it has that key."
	case len(vms) == 1:
		return vms[0], ""
	case len(vms) == 0:
		return "", "a custom resource joins the deployment of a VM step before it, and there is none."
	}
	return "", "name the VM step whose deployment it joins (resource.deployment): several come before it."
}

// bindResourceSteps gives a composite's resource steps the deployment of
// the VM step they join, by its key.
func bindResourceSteps(steps []provision.CompositeStep, deployments map[string]string) {
	for i := range steps {
		if r := steps[i].Resource; r != nil {
			r.DeploymentID = deployments[r.Step]
		}
		if b := steps[i].Branch; b != nil {
			for _, side := range b.Sides() {
				bindResourceSteps(side, deployments)
			}
		}
	}
}

// checkResourceRefs checks, to publish a version, the resource types its
// blueprint names.
func (s *Service) checkResourceRefs(ctx context.Context, d api.CatalogVersionDefinition, prefix string, publish bool) ([]api.FieldError, error) {
	raw, _ := json.Marshal(d.Blueprint)
	b, err := blueprint.Parse(raw)
	if err != nil {
		return nil, nil
	}
	var errs []api.FieldError
	check := func(at, key string) error {
		if key == "" || !publish {
			return nil
		}
		t, err := s.Store.GetResourceType(ctx, key)
		switch {
		case errors.Is(err, store.ErrNotFound):
			errs = append(errs, fieldErr(at, "unknown_resource_type", "There is no resource type "+key+": add it under Administration > Resource types."))
		case err != nil:
			return err
		case !t.Enabled:
			errs = append(errs, fieldErr(at, "disabled", t.Title+" is turned off: turn it on before publishing."))
		}
		return nil
	}
	ptr := prefix + "/blueprint"
	for i, r := range b.Resources {
		if err := check(fmt.Sprintf("%s/resources/%d/type", ptr, i), r.Type); err != nil {
			return nil, err
		}
	}
	var failed error
	eachStep(b, ptr, func(st blueprint.CompositeStep, at string) {
		if st.Resource != nil && failed == nil {
			failed = check(at+"/resource/type", st.Resource.Type)
		}
	})
	return errs, failed
}

// compareResources plans the deployment's custom resources (ADR-0056):
// those the new version adds are made, those it no longer has deleted, and
// those whose inputs change updated by their type's update step. A
// composite's are its own and an update leaves them.
func (s *Service) compareResources(ctx context.Context, u *updatePlan, old blueprint.Blueprint, oldInputs map[string]any, resources []store.Resource) error {
	ev := u.ev
	had := map[string]store.Resource{}
	props := map[string]store.CustomProps{}
	for _, r := range resources {
		if r.Type != day2.CustomType {
			continue
		}
		var p store.CustomProps
		_ = json.Unmarshal(r.Properties, &p)
		if p.Source == "composite" {
			continue
		}
		had[p.Key], props[p.Key] = r, p
	}
	oldWith := map[string]map[string]any{}
	for _, r := range old.Resources {
		oldWith[r.Key] = r.With
	}
	vmsChange := len(u.added) > 0 || len(u.removed) > 0
	changed := map[string]bool{}
	remove := func(r store.Resource) {
		if r.PreventDelete {
			u.refused = append(u.refused, api.UpdateRefusal{Setting: present.Ptr("resources"), Reason: r.Name + " is protected from deletion: allow its deletion first."})
		}
		u.removeFirst = append(u.removeFirst, day2.ResourceOp("delete", day2Resource(r)))
		u.custom = append(u.custom, api.UpdateChange{Kind: api.ResourceRemove, Title: "Delete " + r.Name,
			Detail: present.Ptr("By its type's delete step: " + props[keyOf(r)].ResourceType.Title + "."), Destructive: true})
	}
	for _, unit := range ev.Resources {
		unit.DeploymentID = u.d.ID
		r, ok := had[unit.Key]
		if ok && props[unit.Key].ResourceType.Key != unit.Type {
			remove(r)
			ok = false
		}
		if !ok {
			changed[unit.Key] = true
			u.resources = append(u.resources, unit)
			u.custom = append(u.custom, api.UpdateChange{Kind: api.ResourceAdd, Title: "Add " + unit.Title,
				Detail: present.Ptr("Made by its type's create step, once the VMs are built.")})
			continue
		}
		if !inputsChange(unit.With, oldWith[unit.Key], oldInputs, ev.Inputs, vmsChange, changed) {
			continue
		}
		changed[unit.Key] = true
		t, err := s.Store.GetResourceType(ctx, unit.Type)
		if err != nil {
			return err
		}
		if t.Lifecycle.Update == nil {
			u.refused = append(u.refused, api.UpdateRefusal{Setting: present.Ptr("resources"),
				Reason: r.Name + "'s inputs change, but " + t.Title + " has no update step: ask an administrator to give it one, or request a new deployment."})
			continue
		}
		unit.Phase, unit.ResourceID = provision.PhaseUpdate, r.ID
		u.resources = append(u.resources, unit)
		u.custom = append(u.custom, api.UpdateChange{Kind: api.ResourceUpdate, Title: "Update " + r.Name,
			Detail: present.Ptr("By its type's update step, once the VMs are built; nothing runs if its inputs come out the same.")})
	}
	keys := slices.Sorted(maps.Keys(had))
	for _, k := range keys {
		if !slices.ContainsFunc(ev.Resources, func(unit provision.ResourceUnit) bool { return unit.Key == k }) {
			remove(had[k])
		}
	}
	return nil
}

// inputsChange says whether a kept resource's inputs may change: its
// bindings do, or what they read (the deployment's inputs, its VMs, the
// resources changed before it).
func inputsChange(with, before, oldInputs, newInputs map[string]any, vmsChange bool, changed map[string]bool) bool {
	if !reflect.DeepEqual(with, before) {
		return true
	}
	for _, v := range with {
		expr, ok := v.(string)
		if !ok {
			continue
		}
		rd, err := policy.ReadsOf(expr)
		if err != nil {
			return true
		}
		if rd.AllInputs && !reflect.DeepEqual(oldInputs, newInputs) {
			return true
		}
		for _, in := range rd.Inputs {
			if !reflect.DeepEqual(oldInputs[in], newInputs[in]) {
				return true
			}
		}
		if (len(rd.Components) > 0 || rd.Loose) && vmsChange {
			return true
		}
		for _, r := range rd.Resources {
			if changed[r] {
				return true
			}
		}
	}
	return false
}

func keyOf(r store.Resource) string {
	var p store.CustomProps
	_ = json.Unmarshal(r.Properties, &p)
	return p.Key
}

func day2Resource(r store.Resource) day2.Resource {
	return day2.Resource{ID: r.ID, Type: r.Type, Name: r.Name, Properties: r.Properties}
}
