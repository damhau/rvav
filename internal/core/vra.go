package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/vra"
	"github.com/damhau/rvav/internal/vraimport"
)

// The vRA importer's side in rvav (P2, ADR-0027): what rvav has, for the
// plans, and the adoption of vRA deployments through onboarding.

// vraRvav is what rvav has that the importer's plans need.
func (s *Service) vraRvav(ctx context.Context, e *vra.Estate, projectMap map[string]string) (vraimport.Rvav, error) {
	r := vraimport.Rvav{ProjectMap: projectMap, VMs: map[string]vraimport.VMRef{}, Now: time.Now()}
	page, err := s.Store.ListProjects(ctx, nil, 10000, nil)
	if err != nil {
		return r, err
	}
	for _, p := range page.Items {
		if p.ArchivedAt != nil {
			continue
		}
		members, err := s.Store.ProjectMemberRoles(ctx, p.ID)
		if err != nil {
			return r, err
		}
		rp := vraimport.RvavProject{ID: p.ID, Name: p.Name, Environments: p.Environments}
		for _, m := range members {
			if m.Role != "viewer" {
				rp.Members = append(rp.Members, m.ID)
			}
		}
		r.Projects = append(r.Projects, rp)
	}
	if r.Users, err = s.Store.UsersByEmail(ctx); err != nil {
		return r, err
	}
	if r.Items, err = s.Store.ItemsBySlug(ctx); err != nil {
		return r, err
	}
	var names []string
	for _, d := range e.Deployments {
		for _, res := range d.Resources {
			if res.Type == vra.MachineType {
				if n, _ := res.Properties["resourceName"].(string); n != "" {
					names = append(names, n)
				}
			}
		}
	}
	vms, err := s.Store.InventoryVMsNamed(ctx, names)
	if err != nil {
		return r, err
	}
	for _, v := range vms {
		if _, dup := r.VMs[v.Name]; dup {
			// Two vCenters have a VM of that name: which one vRA built isn't
			// known by name alone.
			r.VMs[v.Name] = vraimport.VMRef{Name: v.Name, Ambiguous: true}
			continue
		}
		r.VMs[v.Name] = vraimport.VMRef{VCenter: v.VCenter, Moref: v.Moref, Name: v.Name, Managed: v.Managed}
	}
	return r, nil
}

// AdoptVRADeployments plans the adoption of vRA's deployments and, with
// apply, adopts those it can: each through onboarding, as one deployment
// of its vRA name, with its owner, project, environment, item and lease
// (P2.3). Administrators only.
func (s *Service) AdoptVRADeployments(ctx context.Context, p *auth.Principal, e *vra.Estate, projectMap map[string]string,
	apply bool) ([]vraimport.DeploymentPlan, error) {
	if !p.IsAdmin() {
		return nil, ErrForbidden
	}
	r, err := s.vraRvav(ctx, e, projectMap)
	if err != nil {
		return nil, err
	}
	plans := vraimport.PlanDeployments(e, r)
	if !apply {
		return plans, nil
	}
	for i, pl := range plans {
		if pl.Status != vraimport.Adopt {
			continue
		}
		d := api.OnboardingDraft{ProjectId: pl.ProjectID, Environment: pl.Environment, OwnerId: pl.OwnerID,
			CatalogItemId: pl.CatalogItemID, LeaseDays: pl.LeaseDays, DeploymentName: &plans[i].Name}
		for _, vm := range pl.VMs {
			d.Vms = append(d.Vms, struct {
				Moref   string `json:"moref"`
				Vcenter string `json:"vcenter"`
			}{Moref: vm.Moref, Vcenter: vm.VCenter})
		}
		res, err := s.AdoptVMs(ctx, p, d)
		var ve *ValidationError
		switch {
		case errors.As(err, &ve):
			var msgs []string
			for _, fe := range ve.Errors {
				msgs = append(msgs, fe.Message)
			}
			plans[i].Status = vraimport.Failed
			plans[i].Notes = append(plans[i].Notes, vraimport.Note{Level: vraimport.Unmapped, Where: "adoption",
				Text: "Onboarding refused it: " + strings.Join(msgs, " ")})
			continue
		case err != nil:
			return plans, err
		}
		plans[i].Status = vraimport.Adopted
		for _, dep := range res.Deployments {
			plans[i].Deployments = append(plans[i].Deployments, dep.Id)
		}
	}
	return plans, nil
}
