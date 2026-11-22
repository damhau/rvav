package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/nsxmanagers"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vraimport"
)

// NSX objects follow the VM's vCenter (ADR-0062): made on the NSX Manager
// serving it, by name, "" for the NSX integration's.

// nsxClient connects to an NSX Manager by name.
func (s *Service) nsxClient(ctx context.Context, name string) (*nsx.Client, error) {
	if name == "" {
		return s.nsx()
	}
	row, err := s.Store.GetNSXManager(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("NSX Manager %s: %w", name, err)
	}
	password, err := nsxmanagers.Password(s.Secrets, row)
	if err != nil {
		return nil, err
	}
	return nsx.New(nsxmanagers.Options(row, password))
}

// nsxManagerOf is the NSX Manager serving a vCenter; false when none does,
// or when it is the integration's and that isn't set.
func (s *Service) nsxManagerOf(ctx context.Context, vcenter string) (string, bool, error) {
	m, ok, err := s.Store.NSXManagerOf(ctx, vcenter)
	if err != nil || !ok || m == "" && !s.Config.NSX.Configured() {
		return "", false, err
	}
	return m, true, nil
}

// allNSXManagers are every NSX Manager's name, the integration's first.
func (s *Service) allNSXManagers(ctx context.Context) ([]string, error) {
	var out []string
	if s.Config.NSX.Configured() {
		out = append(out, "")
	}
	rows, err := s.Store.NSXManagers(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out, nil
}

// servingNSXManagers are the NSX Managers that serve at least one vCenter,
// the integration's first.
func (s *Service) servingNSXManagers(ctx context.Context) ([]string, error) {
	all, err := s.allNSXManagers(ctx)
	if err != nil {
		return nil, err
	}
	links, err := s.Store.VCenterNSX(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(m string) bool {
		for _, l := range links {
			if l == m {
				return false
			}
		}
		return true
	}), nil
}

// deploymentNSX is the NSX Manager a deployment's security groups are
// on; false when it has none.
func deploymentNSX(resources []store.Resource) (string, bool) {
	for _, r := range resources {
		if r.Type == "nsx.group" {
			var p struct {
				NSX string `json:"nsx_manager"`
			}
			_ = json.Unmarshal(r.Properties, &p)
			return p.NSX, true
		}
	}
	return "", false
}

// adoptionNSX is the NSX Manager serving the vCenter of an adopted
// deployment's VMs; why says why there is none.
func (s *Service) adoptionNSX(ctx context.Context, vms []vraimport.VMRef) (string, string, error) {
	all, err := s.allNSXManagers(ctx)
	if err != nil || len(all) == 0 {
		return "", "rvav has no NSX Manager", err
	}
	var vcenters []string
	for _, vm := range vms {
		if !slices.Contains(vcenters, vm.VCenter) {
			vcenters = append(vcenters, vm.VCenter)
		}
	}
	if len(vcenters) == 0 {
		return all[0], "", nil
	}
	manager := ""
	for i, vc := range vcenters {
		m, ok, err := s.nsxManagerOf(ctx, vc)
		switch {
		case err != nil:
			return "", "", err
		case !ok:
			return "", "No NSX Manager serves vCenter " + vc, nil
		case i > 0 && m != manager:
			return "", "Its VMs are in vCenters different NSX Managers serve", nil
		}
		manager = m
	}
	return manager, "", nil
}

// nsxLabel is how a message names an NSX Manager.
func (s *Service) nsxLabel(name string) string {
	if name == "" {
		return s.primaryNSXName()
	}
	return name
}

// segmentsFor is what the first placement rule that makes segments on
// the NSX Manager serving this vCenter says: one naming no vCenter, or a
// vCenter that NSX Manager serves. False when that NSX Manager has none;
// nil when no NSX Manager serves the vCenter.
func (s *Service) segmentsFor(ctx context.Context, pl *placement, vcenter string) (*store.PlacementSegments, string, bool, error) {
	m, ok, err := s.nsxManagerOf(ctx, vcenter)
	if err != nil || !ok {
		return nil, "", true, err
	}
	for _, r := range pl.rules {
		if r.Segments == nil {
			continue
		}
		if r.VCenter != "" {
			rm, rok, err := s.nsxManagerOf(ctx, r.VCenter)
			if err != nil {
				return nil, "", false, err
			}
			if !rok || rm != m {
				continue
			}
		}
		sg := *r.Segments
		return &sg, m, true, nil
	}
	return nil, m, false, nil
}

// needsGroups says whether VMs of the component join NSX security groups:
// the blueprint's, or existing ones a component or this plan names.
func (ev *Evaluation) needsGroups(c blueprint.Component, plan blueprint.VMPlan) bool {
	existing := func(g string) bool {
		_, ok := blueprint.ExistingGroup(g)
		return ok
	}
	return len(ev.Blueprint.SecurityGroups) > 0 || slices.ContainsFunc(slices.Concat(c.Groups, plan.Groups), existing) ||
		slices.ContainsFunc(ev.Blueprint.Components, func(o blueprint.Component) bool { return slices.ContainsFunc(o.Groups, existing) })
}

// nsxServing says which vCenters of the clusters placement may choose an
// NSX Manager serves; nil when rvav has none, which the request's
// evaluation says.
func (s *Service) nsxServing(ctx context.Context, pl *placement) (func(string) bool, error) {
	all, err := s.allNSXManagers(ctx)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	served := map[string]bool{}
	for _, c := range pl.clusters {
		if _, seen := served[c.VCenter]; seen {
			continue
		}
		_, ok, err := s.nsxManagerOf(ctx, c.VCenter)
		if err != nil {
			return nil, err
		}
		served[c.VCenter] = ok
	}
	return func(vc string) bool { return served[vc] }, nil
}

// evaluateNSXManager picks the NSX Manager the request's segments and
// security groups are made on: the one serving its VMs' vCenter, one for
// them all. A VM that needs NSX in a vCenter no NSX Manager serves is
// refused here, before anything is made.
func (s *Service) evaluateNSXManager(ctx context.Context, ev *Evaluation) error {
	add := func(code, msg string) { ev.Errors = append(ev.Errors, fieldErr("", code, msg)) }
	ev.nsxManager, ev.nsxFound = "", false
	groups := len(ev.Blueprint.SecurityGroups) > 0
	for _, p := range ev.Parts {
		groups = groups || slices.ContainsFunc(slices.Concat(p.comp.Groups, p.vm.Groups), func(g string) bool {
			_, ok := blueprint.ExistingGroup(g)
			return ok
		})
	}
	needed, at := false, ""
	for _, p := range ev.Parts {
		if p.count == 0 || p.segments == nil && !groups {
			continue
		}
		needed = true
		if p.vcenter == "" {
			continue // not placed: said where it is evaluated
		}
		m, ok, err := s.nsxManagerOf(ctx, p.vcenter)
		if err != nil {
			return err
		}
		switch {
		case !ok:
			if all, err := s.allNSXManagers(ctx); err != nil {
				return err
			} else if len(all) == 0 {
				add("no_nsx", "This item makes NSX segments or security groups for its VMs, and rvav has no NSX Manager. "+
					"Ask an administrator to set one in Integrations.")
				return nil
			}
			add("no_nsx", fmt.Sprintf("%s's VMs go to vCenter %s, which no NSX Manager serves: their NSX segments and security groups "+
				"can't be made. Ask an administrator to link one to it in Integrations › NSX.", p.title(), p.vcenter))
			return nil
		case ev.nsxFound && m != ev.nsxManager:
			add("nsx_managers", fmt.Sprintf("Its VMs go to vCenters %s and %s, which different NSX Managers serve (%s and %s): one "+
				"request's NSX segments and security groups are made on one. Choose clusters of vCenters one NSX Manager serves.",
				at, p.vcenter, s.nsxLabel(ev.nsxManager), s.nsxLabel(m)))
			return nil
		}
		ev.nsxManager, ev.nsxFound, at = m, true, p.vcenter
	}
	if ev.nsxFound || !needed && !groups {
		return nil
	}
	// Not placed yet: the NSX Manager there is, as when rvav had one.
	all, err := s.allNSXManagers(ctx)
	if err != nil {
		return err
	}
	switch {
	case len(all) > 0:
		ev.nsxManager, ev.nsxFound = all[0], true
	case groups:
		add("no_nsx", "This item puts its VMs in NSX security groups, and rvav has no NSX Manager. Ask an administrator to set one in Integrations.")
	}
	return nil
}
