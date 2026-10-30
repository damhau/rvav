package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vra"
	"github.com/damhau/rvav/internal/vraimport"
)

// The vRA importer's side in rvav (ADR-0027): the estate read from vRA,
// the dry run, the import that can run again, and the adoption of vRA
// deployments through onboarding. Administrators only.

// vraRvav is what rvav has that the importer's plans need.
func (s *Service) vraRvav(ctx context.Context, e *vra.Estate, projectMap map[string]string) (vraimport.Rvav, error) {
	r := vraimport.Rvav{ProjectMap: projectMap, VMs: map[string]vraimport.VMRef{}, TemplateItems: map[string]string{}, Now: time.Now()}
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

// vraPlan is the dry run of an estate: what earlier imports made, the
// project mapping (administrators', then extra), and what rvav has.
func (s *Service) vraPlan(ctx context.Context, e *vra.Estate, extra map[string]string) (vraimport.ImportPlan, vraimport.Rvav, error) {
	pm, err := s.Store.VRAProjectMap(ctx)
	if err != nil {
		return vraimport.ImportPlan{}, vraimport.Rvav{}, err
	}
	for k, v := range extra {
		pm[k] = v
	}
	r, err := s.vraRvav(ctx, e, pm)
	if err != nil {
		return vraimport.ImportPlan{}, r, err
	}
	states, err := s.Store.ItemImportStates(ctx)
	if err != nil {
		return vraimport.ImportPlan{}, r, err
	}
	st := vraimport.ImportState{Links: map[string]vraimport.Link{}, Items: map[string]vraimport.ItemState{}, Slugs: map[string]string{}}
	for id, it := range states {
		st.Items[id] = vraimport.ItemState{ID: it.ID, Slug: it.Slug, HasDraft: it.HasDraft, AllProjects: it.AllProjects, Projects: it.Projects}
		st.Slugs[it.Slug] = id
	}
	links, err := s.Store.VRALinks(ctx, "template")
	if err != nil {
		return vraimport.ImportPlan{}, r, err
	}
	// The latest link of a template whose item is still there.
	for _, l := range links {
		if _, ok := st.Items[l.RvavID]; ok {
			st.Links[l.VRAID] = vraimport.Link{RvavID: l.RvavID, Version: l.VRAVersion, Hash: l.SourceHash}
			r.TemplateItems[l.VRAID] = l.RvavID
		}
	}
	return vraimport.PlanImport(e, r, st), r, nil
}

// adoptPlans adopts the deployments planned for it, through onboarding.
func (s *Service) adoptPlans(ctx context.Context, p *auth.Principal, plans []vraimport.DeploymentPlan) ([]vraimport.DeploymentPlan, error) {
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
		var ce *ConflictError
		switch {
		case errors.As(err, &ve), errors.As(err, &ce):
			plans[i].Status = vraimport.Failed
			plans[i].Notes = append(plans[i].Notes, vraimport.Note{Level: vraimport.Unmapped, Where: "adoption",
				Text: "Onboarding refused it: " + err.Error() + " " + fieldMessages(ve)})
			continue
		case err != nil:
			return plans, err
		}
		plans[i].Status = vraimport.Adopted
		for _, dep := range res.Deployments {
			plans[i].Deployments = append(plans[i].Deployments, dep.Id)
			if err := s.Store.SaveVRALink(ctx, store.VRALink{Kind: "deployment", VRAID: pl.VRAID, RvavID: dep.Id}, p.User.ID); err != nil {
				return plans, err
			}
		}
	}
	return plans, nil
}

func fieldMessages(ve *ValidationError) string {
	if ve == nil || len(ve.Errors) < 2 {
		return ""
	}
	var msgs []string
	for _, fe := range ve.Errors {
		msgs = append(msgs, fe.Message)
	}
	return strings.Join(msgs, " ")
}

// AdoptVRADeployments plans the adoption of vRA's deployments and, with
// apply, adopts those it can: each through onboarding, as one deployment
// of its vRA name, with its owner, project, environment, item and lease
// (P2.3). projectMap adds to the administrators' mapping.
func (s *Service) AdoptVRADeployments(ctx context.Context, p *auth.Principal, e *vra.Estate, projectMap map[string]string,
	apply bool) ([]vraimport.DeploymentPlan, error) {
	if !p.IsAdmin() {
		return nil, ErrForbidden
	}
	plan, _, err := s.vraPlan(ctx, e, projectMap)
	if err != nil || !apply {
		return plan.Deployments, err
	}
	return s.adoptPlans(ctx, p, plan.Deployments)
}

// ── The importer's API (P2.5) ──────────────────────────────────────────

func estateCounts(e *vra.Estate) json.RawMessage {
	b, _ := json.Marshal(map[string]int{"projects": len(e.Projects), "templates": len(e.Blueprints), "catalog_items": len(e.CatalogItems),
		"deployments": len(e.Deployments), "policies": len(e.Policies), "subscriptions": len(e.Subscriptions),
		"vro_workflows": len(e.Workflows), "vro_actions": len(e.Actions)})
	return b
}

func presentVRASnapshot(v store.VRASnapshot) api.VraSnapshot {
	out := api.VraSnapshot{Id: v.ID, ReadAt: v.ReadAt, Source: api.VraSnapshotSource(v.Source), CreatedAt: v.CreatedAt, CreatedBy: v.CreatedByName}
	_ = json.Unmarshal(v.Counts, &out.Counts)
	if v.URL != "" {
		out.Url = &v.URL
	}
	return out
}

// latestEstate is the estate read last.
func (s *Service) latestEstate(ctx context.Context) (store.VRASnapshot, *vra.Estate, error) {
	v, err := s.Store.LatestVRASnapshot(ctx, true)
	if errors.Is(err, store.ErrNotFound) {
		return v, nil, ErrNotFound
	}
	if err != nil {
		return v, nil, err
	}
	var e vra.Estate
	if err := json.Unmarshal(v.Estate, &e); err != nil {
		return v, nil, err
	}
	return v, &e, nil
}

// VRAImportStatus says whether rvav can read vRA, and what it read last.
func (s *Service) VRAImportStatus(ctx context.Context, p *auth.Principal) (api.VraImportStatus, error) {
	if !p.IsAdmin() {
		return api.VraImportStatus{}, ErrForbidden
	}
	out := api.VraImportStatus{Configured: s.Config.VRA.URL != ""}
	if out.Configured {
		out.Url = &s.Config.VRA.URL
	}
	v, err := s.Store.LatestVRASnapshot(ctx, false)
	if err == nil {
		snap := presentVRASnapshot(v)
		out.Snapshot = &snap
	} else if !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	return out, nil
}

// ReadVRA reads vRA (no body) or takes the estate vra-export wrote, as the
// estate the plan and import work from.
func (s *Service) ReadVRA(ctx context.Context, p *auth.Principal, body []byte) (api.VraSnapshot, error) {
	if !p.IsAdmin() {
		return api.VraSnapshot{}, ErrForbidden
	}
	var e *vra.Estate
	snap := store.VRASnapshot{ID: ids.New(ids.VRASnapshot), CreatedBy: &p.User.ID}
	if len(bytes.TrimSpace(body)) == 0 {
		c := s.Config.VRA
		if c.URL == "" {
			return api.VraSnapshot{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "not_configured",
				"rvav has no vRA to read: set RVAV_VRA_URL, or give the file rvav vra-export wrote.")}}
		}
		client, err := vra.New(vra.Options{URL: c.URL, Token: c.Token, Username: c.Username, Password: c.Password, Domain: c.Domain,
			Insecure: c.Insecure})
		if err != nil {
			return api.VraSnapshot{}, &UpstreamError{System: "vRA", Err: err}
		}
		if e, err = client.Read(ctx); err != nil {
			return api.VraSnapshot{}, &UpstreamError{System: "vRA", Err: err}
		}
		snap.Source = "vra"
	} else {
		e = &vra.Estate{}
		if err := json.Unmarshal(body, e); err != nil {
			return api.VraSnapshot{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "invalid",
				"This is not what rvav vra-export writes: "+err.Error())}}
		}
		if len(e.Projects) == 0 && len(e.Blueprints) == 0 && len(e.Deployments) == 0 && len(e.Workflows) == 0 {
			return api.VraSnapshot{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "empty",
				"The file has no vRA projects, templates, deployments or workflows.")}}
		}
		if e.ReadAt.IsZero() {
			e.ReadAt = time.Now().UTC()
		}
		snap.Source = "file"
	}
	snap.ReadAt, snap.URL, snap.Counts = e.ReadAt, e.URL, estateCounts(e)
	var err error
	if snap.Estate, err = json.Marshal(e); err != nil {
		return api.VraSnapshot{}, err
	}
	if err := s.Store.SaveVRASnapshot(ctx, snap); err != nil {
		return api.VraSnapshot{}, err
	}
	s.audit(ctx, p, "vra.read", "vra_snapshot", snap.ID, nil, map[string]any{"source": snap.Source, "url": snap.URL})
	v, err := s.Store.LatestVRASnapshot(ctx, false)
	return presentVRASnapshot(v), err
}

// convert carries a value over to the API type of the same JSON.
func convert[T any](v any) (T, error) {
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(b, &out)
}

func presentVRAPlan(v store.VRASnapshot, plan vraimport.ImportPlan) (api.VraPlan, error) {
	out, err := convert[api.VraPlan](plan)
	out.Snapshot = presentVRASnapshot(v)
	return out, err
}

// VRAPlan is the dry run of the estate read last.
func (s *Service) VRAPlan(ctx context.Context, p *auth.Principal) (api.VraPlan, error) {
	if !p.IsAdmin() {
		return api.VraPlan{}, ErrForbidden
	}
	v, e, err := s.latestEstate(ctx)
	if err != nil {
		return api.VraPlan{}, err
	}
	plan, _, err := s.vraPlan(ctx, e, nil)
	if err != nil {
		return api.VraPlan{}, err
	}
	return presentVRAPlan(v, plan)
}

// MapVRAProject maps a vRA project of the estate to an rvav project, or
// clears its mapping.
func (s *Service) MapVRAProject(ctx context.Context, p *auth.Principal, vraID string, m api.VraProjectMapping) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	_, e, err := s.latestEstate(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(e.Projects, func(x vra.Project) bool { return x.ID == vraID }) {
		return ErrNotFound
	}
	to := ""
	if m.RvavProjectId != nil {
		to = *m.RvavProjectId
		unknown, err := s.Store.UnknownProjects(ctx, []string{to})
		if err != nil {
			return err
		}
		if len(unknown) > 0 {
			return &ValidationError{Errors: []api.FieldError{fieldErr("/rvav_project_id", "unknown", "No such project: "+to+".")}}
		}
	}
	if err := s.Store.SetVRAProjectMap(ctx, vraID, to, p.User.ID); err != nil {
		return err
	}
	s.audit(ctx, p, "vra.project.map", "vra_project", vraID, nil, map[string]any{"rvav_project_id": to})
	return nil
}

// ImportVRA imports the estate read last: its templates as catalog items
// (drafts) with their sharing, and, when asked, its deployments adopted.
// It can run again: what was imported is linked, so only what changed is.
func (s *Service) ImportVRA(ctx context.Context, p *auth.Principal, req api.VraImportRequest) (api.VraImportResult, error) {
	out := api.VraImportResult{Created: []string{}, Updated: []string{}, Adopted: []string{}}
	out.Failed = []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}{}
	if !p.IsAdmin() {
		return out, ErrForbidden
	}
	v, e, err := s.latestEstate(ctx)
	if err != nil {
		return out, err
	}
	fail := func(name string, err error) {
		out.Failed = append(out.Failed, struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		}{name, err.Error() + " " + fieldMessages(asValidation(err))})
	}
	plan, _, err := s.vraPlan(ctx, e, nil)
	if err != nil {
		return out, err
	}
	if req.Catalog == nil || *req.Catalog {
		for _, it := range plan.Items {
			itemID := it.RvavItemID
			switch it.Action {
			case vraimport.ActionCreate, vraimport.ActionUpdate:
				y, err := it.Doc.YAML()
				if err != nil {
					return out, err
				}
				target := ""
				if it.Action == vraimport.ActionUpdate {
					target = it.RvavItemID
				}
				res, err := s.ImportCatalogItem(ctx, p, y, target)
				if isRefusal(err) {
					fail(it.Name, err)
					continue
				} else if err != nil {
					return out, err
				}
				itemID = res.ItemId
				if err := s.Store.SaveVRALink(ctx, store.VRALink{Kind: "template", VRAID: it.BlueprintID, RvavID: itemID,
					VRAVersion: it.Version, SourceHash: it.Hash}, p.User.ID); err != nil {
					return out, err
				}
				if it.Action == vraimport.ActionCreate {
					out.Created = append(out.Created, it.Name)
				} else {
					out.Updated = append(out.Updated, it.Name)
				}
			case vraimport.ActionUnchanged:
			case vraimport.ActionConflict:
				if itemID == "" {
					continue // a slug in the way: nothing to share
				}
			default:
				continue
			}
			if len(it.ShareAdd) == 0 {
				continue
			}
			cur, err := s.CatalogItemEntitlements(ctx, p, itemID)
			if err != nil {
				return out, err
			}
			if cur.AllProjects {
				continue
			}
			cur.ProjectIds = slices.Compact(slices.Sorted(slices.Values(append(cur.ProjectIds, it.ShareAdd...))))
			if _, err := s.SetCatalogItemEntitlements(ctx, p, itemID, cur); err != nil {
				return out, err
			}
			out.Shared += len(it.ShareAdd)
		}
	}
	if req.Deployments != nil && *req.Deployments {
		// Planned again: deployments find the items just imported.
		again, _, err := s.vraPlan(ctx, e, nil)
		if err != nil {
			return out, err
		}
		adopted, err := s.adoptPlans(ctx, p, again.Deployments)
		if err != nil {
			return out, err
		}
		for _, d := range adopted {
			switch {
			case d.Status == vraimport.Adopted && len(d.Deployments) > 0:
				out.Adopted = append(out.Adopted, d.Name)
			case d.Status == vraimport.Failed:
				fail(d.Name, errors.New(d.Notes[len(d.Notes)-1].Text))
			}
		}
	}
	s.audit(ctx, p, "vra.import", "vra_snapshot", v.ID, nil, map[string]any{"created": out.Created, "updated": out.Updated,
		"shared": out.Shared, "adopted": out.Adopted, "failed": len(out.Failed)})
	after, _, err := s.vraPlan(ctx, e, nil)
	if err != nil {
		return out, err
	}
	out.Plan, err = presentVRAPlan(v, after)
	return out, err
}

// VROTriageMarkdown is the vRO triage report of the estate read last.
func (s *Service) VROTriageMarkdown(ctx context.Context, p *auth.Principal) (string, error) {
	if !p.IsAdmin() {
		return "", ErrForbidden
	}
	_, e, err := s.latestEstate(ctx)
	if err != nil {
		return "", err
	}
	return vraimport.TriageVRO(e).Markdown(), nil
}

// isRefusal: the import refused it (checks, a conflict), not a failure.
func isRefusal(err error) bool {
	var ve *ValidationError
	var ce *ConflictError
	return errors.As(err, &ve) || errors.As(err, &ce)
}

func asValidation(err error) *ValidationError {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve
	}
	return nil
}
