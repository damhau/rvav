package core

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/present"
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
		members, err := s.requesters(ctx, p.ID)
		if err != nil {
			return r, err
		}
		rp := vraimport.RvavProject{ID: p.ID, Name: p.Name, Environments: p.Environments, Members: members, Properties: p.Properties,
			DeploymentSharing: p.DeploymentSharing}
		r.Projects = append(r.Projects, rp)
	}
	secrets, err := s.Store.Secrets(ctx)
	if err != nil {
		return r, err
	}
	r.Secrets = map[string]bool{}
	for _, v := range secrets {
		r.Secrets[v.Name] = true
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
		if prev, dup := r.VMs[v.Name]; dup {
			// Two vCenters have a VM of that name: which one vRA built isn't
			// known by name alone. One rvav manages is adopted, though.
			r.VMs[v.Name] = vraimport.VMRef{Name: v.Name, Ambiguous: true, Managed: prev.Managed || v.Managed}
			continue
		}
		r.VMs[v.Name] = vraimport.VMRef{VCenter: v.VCenter, Moref: v.Moref, Name: v.Name, Managed: v.Managed}
	}
	if r.Adopted, err = s.Store.AdoptedVRADeployments(ctx); err != nil {
		return r, err
	}
	if err := s.vraImages(ctx, &r); err != nil {
		return r, err
	}
	if err := s.vraFormLibrary(ctx, &r); err != nil {
		return r, err
	}
	// The lease policies adoption will apply.
	r.Leases = map[string]vraimport.Lease{}
	for _, p := range r.Projects {
		for _, env := range p.Environments {
			l, err := s.Store.LeasePolicy(ctx, p.ID, env)
			if errors.Is(err, store.ErrNotFound) {
				continue
			} else if err != nil {
				return r, err
			}
			r.Leases[p.ID+"/"+env] = vraimport.Lease{Default: l.DefaultDays, Max: l.MaxDays}
		}
	}
	return r, nil
}

// vraImages gives the plan the vCenters rvav manages, by name and URL host,
// and its catalogue templates (ADR-0037).
func (s *Service) vraImages(ctx context.Context, r *vraimport.Rvav) error {
	added, err := s.Store.VCenters(ctx)
	if err != nil {
		return err
	}
	if s.Config.VCenter.Name != "" || s.Config.VCenter.URL != "" {
		r.VCenters = append(r.VCenters, vraimport.RvavVCenter{Name: s.Config.VCenter.Name, Host: vraimport.URLHost(s.Config.VCenter.URL)})
	}
	for _, v := range added {
		r.VCenters = append(r.VCenters, vraimport.RvavVCenter{Name: v.Name, Host: vraimport.URLHost(v.URL)})
	}
	list, err := s.Store.VMTemplates(ctx)
	if err != nil {
		return err
	}
	r.Templates = map[string]vraimport.RvavTemplate{}
	for _, t := range list {
		rt := vraimport.RvavTemplate{UserData: t.UserData, Constraints: slices.Clone(t.Constraints.Compute)}
		for _, src := range t.Sources {
			tpl := src.Template
			if src.Library != "" {
				tpl = src.Library + "/" + tpl
			}
			rt.Sources = append(rt.Sources, vraimport.TemplateSource{VCenter: cmp.Or(src.VCenter, s.Config.VCenter.Name), Template: tpl})
		}
		// Only compute constraints come from vRA: others make it differ.
		for _, c := range slices.Concat(t.Constraints.Storage, t.Constraints.Network) {
			rt.Constraints = append(rt.Constraints, "other:"+c)
		}
		r.Templates[t.Key] = rt
	}
	groups, err := s.Store.PropertyGroups(ctx)
	if err != nil {
		return err
	}
	r.PropertyGroups = map[string]vraimport.RvavGroup{}
	for _, g := range groups {
		rg := vraimport.RvavGroup{Version: g.Version}
		if err := json.Unmarshal(g.Inputs, &rg.Inputs); err != nil {
			return err
		}
		if err := json.Unmarshal(g.Constants, &rg.Constants); err != nil {
			return err
		}
		r.PropertyGroups[g.Key] = rg
	}
	return nil
}

// vraFormLibrary gives the plan rvav's form library and the hosts REST
// sources may call (ADR-0043).
func (s *Service) vraFormLibrary(ctx context.Context, r *vraimport.Rvav) error {
	entries, err := s.Store.LibraryEntries(ctx)
	if err != nil {
		return err
	}
	r.FormLibrary = map[string]vraimport.RvavEntry{}
	for _, e := range entries {
		re := vraimport.RvavEntry{Version: e.Version, Use: e.Use, Kind: e.Kind, Description: e.Description}
		if err := json.Unmarshal(e.Params, &re.Params); err != nil {
			return err
		}
		var params []api.FormLibraryParameter
		if err := json.Unmarshal(e.Parameters, &params); err != nil {
			return err
		}
		for _, p := range params {
			re.Parameters = append(re.Parameters, p.Name)
		}
		r.FormLibrary[e.Key] = re
	}
	r.RESTAllowed = func(target string) bool {
		u, err := url.Parse(target)
		return err == nil && s.REST.Allowed(ctx, u)
	}
	return nil
}

// vraLibraryEntry makes a form library entry of a vRO action (ADR-0043).
func (s *Service) vraLibraryEntry(ctx context.Context, p *auth.Principal, li vraimport.LibraryImport) error {
	entry := maps.Clone(li.Entry)
	entry["key"] = li.Key
	w, err := convert[api.FormLibraryEntryWrite](entry)
	if err != nil {
		return err
	}
	_, err = s.PutFormLibraryEntry(ctx, p, "", w, true)
	if errors.Is(err, ErrForbidden) {
		return &ValidationError{Errors: []api.FieldError{fieldErr("", "forbidden", "Making form library entries needs the right to manage the catalog.")}}
	}
	return err
}

// vraPropertyGroup makes a property group of vRA's (ADR-0038).
func (s *Service) vraPropertyGroup(ctx context.Context, p *auth.Principal, g vraimport.GroupImport) error {
	w, err := convert[api.PropertyGroupWrite](map[string]any{"key": g.Key, "name": g.Label, "description": g.Description,
		"inputs": g.Inputs, "constants": g.Constants})
	if err != nil {
		return err
	}
	_, err = s.PutPropertyGroup(ctx, p, "", w, true)
	if errors.Is(err, ErrForbidden) {
		return &ValidationError{Errors: []api.FieldError{fieldErr("", "forbidden", "Making property groups needs the right to manage the catalog.")}}
	}
	return err
}

// vraTemplate makes a catalogue template of vRA's image mappings, offered
// to every project and environment as vRA's are to every project in their
// regions.
func (s *Service) vraTemplate(ctx context.Context, p *auth.Principal, t vraimport.TemplateImport) error {
	sources := make([]api.VmTemplateSourceWrite, len(t.Sources))
	for i, src := range t.Sources {
		sources[i] = api.VmTemplateSourceWrite{Vcenter: src.VCenter, Template: src.Template}
	}
	_, err := s.SaveVMTemplate(ctx, p, "", api.VmTemplateWrite{Key: t.Key, Label: t.Label, OsFamily: api.VmTemplateWriteOsFamily(t.OSFamily),
		Sources: sources, Description: present.Ptr("vRA's image mappings of " + t.Image + "."), UserData: &t.UserData,
		Constraints: &api.TemplateConstraints{Compute: t.Constraints, Storage: []string{}, Network: []string{}}, Offers: &[]api.Offer{{}}})
	if errors.Is(err, ErrForbidden) {
		return &ValidationError{Errors: []api.FieldError{fieldErr("", "forbidden", "Making catalogue templates needs the right to manage infrastructure.")}}
	}
	return err
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
		st.Items[id] = vraimport.ItemState{ID: it.ID, Slug: it.Slug, HasDraft: it.HasDraft, Published: it.Published,
			AllProjects: it.AllProjects, Projects: it.Projects}
		st.Slugs[it.Slug] = id
	}
	moves, err := s.Store.VRAMoves(ctx)
	if err != nil {
		return vraimport.ImportPlan{}, r, err
	}
	st.Cutover = map[string]vraimport.Moved{}
	for id, m := range moves {
		st.Cutover[id] = vraimport.Moved{At: m.MovedAt, By: deref(m.MovedBy), Note: m.Note}
	}
	links, err := s.Store.VRALinks(ctx, "template")
	if err != nil {
		return vraimport.ImportPlan{}, r, err
	}
	shares, err := s.Store.VRALinks(ctx, "share")
	if err != nil {
		return vraimport.ImportPlan{}, r, err
	}
	st.Shared = map[string]bool{}
	for _, l := range shares {
		st.Shared[l.RvavID] = true
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
			CatalogItemId: pl.CatalogItemID, LeaseDays: pl.LeaseDays, DeploymentName: &plans[i].RvavName}
		for _, vm := range pl.VMs {
			d.Vms = append(d.Vms, struct {
				Moref   string `json:"moref"`
				Vcenter string `json:"vcenter"`
			}{Moref: vm.Moref, Vcenter: vm.VCenter})
		}
		res, err := s.AdoptVMs(ctx, p, d)
		var ve *ValidationError
		var ce *ConflictError
		var ue *UpstreamError
		switch {
		case errors.As(err, &ve), errors.As(err, &ce):
			plans[i].Status = vraimport.Failed
			plans[i].Notes = append(plans[i].Notes, vraimport.Note{Level: vraimport.Unmapped, Where: "adoption",
				Text: "Onboarding refused it: " + err.Error() + " " + fieldMessages(ve)})
			continue
		case errors.As(err, &ue):
			// vCenter or EfficientIP didn't answer: this one waits for the
			// next run, the others go on.
			plans[i].Status = vraimport.Failed
			plans[i].Notes = append(plans[i].Notes, vraimport.Note{Level: vraimport.Unmapped, Where: "adoption",
				Text: "Not adopted: " + err.Error() + ". Run the import again."})
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
		// Its NSX segments and groups come with it (ADR-0028).
		if len(res.Deployments) > 0 {
			notes, err := s.adoptNSX(ctx, res.Deployments[0].Id, pl)
			var ue *UpstreamError
			switch {
			case errors.As(err, &ue):
				plans[i].Notes = append(plans[i].Notes, vraimport.Note{Level: vraimport.Warning, Where: "nsx",
					Text: "Its NSX segments and groups weren't adopted: " + err.Error() + "."})
			case err != nil:
				return plans, err
			}
			plans[i].Notes = append(plans[i].Notes, notes...)
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
	if !p.Everywhere(auth.ImportManage) {
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
	if !p.Everywhere(auth.ImportManage) {
		return api.VraImportStatus{}, ErrForbidden
	}
	c := s.liveConfig(ctx).VRA
	out := api.VraImportStatus{Configured: c.URL != ""}
	if out.Configured {
		out.Url = &c.URL
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

// ReadVRA reads vRA (an empty body) or takes the estate vra-export wrote,
// as the estate the plan and import work from. The file is decoded as it
// comes, not held whole.
func (s *Service) ReadVRA(ctx context.Context, p *auth.Principal, body io.Reader) (api.VraSnapshot, error) {
	if !p.Everywhere(auth.ImportManage) {
		return api.VraSnapshot{}, ErrForbidden
	}
	var e *vra.Estate
	snap := store.VRASnapshot{ID: ids.New(ids.VRASnapshot), CreatedBy: &p.User.ID}
	in := bufio.NewReader(body)
	empty := true
	for {
		c, err := in.ReadByte()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return api.VraSnapshot{}, err
			}
			break
		}
		if !unicode.IsSpace(rune(c)) {
			_ = in.UnreadByte()
			empty = false
			break
		}
	}
	if empty {
		c := s.liveConfig(ctx).VRA
		if c.URL == "" {
			return api.VraSnapshot{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "not_configured",
				"rvav has no vRA to read: set it in Administration › Integrations › vRA, or give the file rvav vra-export wrote.")}}
		}
		client, err := vra.New(vra.Options{URL: c.URL, Token: c.Token, Username: c.Username, Password: c.Password, Domain: c.Domain,
			Insecure: c.Insecure, Pin: c.CertSHA256})
		if err != nil {
			return api.VraSnapshot{}, &UpstreamError{System: "vRA", Err: err}
		}
		if e, err = client.Read(ctx); err != nil {
			return api.VraSnapshot{}, &UpstreamError{System: "vRA", Err: err}
		}
		snap.Source = "vra"
	} else {
		e = &vra.Estate{}
		if err := json.NewDecoder(in).Decode(e); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return api.VraSnapshot{}, err
			}
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
	if !p.Everywhere(auth.ImportManage) {
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
	if !p.Everywhere(auth.ImportManage) {
		return ErrForbidden
	}
	_, e, err := s.latestEstate(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(e.Projects, func(x vra.Project) bool { return x.ID == vraID }) {
		return ErrNotFound
	}
	to, stay := "", m.StaysInVra != nil && *m.StaysInVra
	if stay && m.RvavProjectId != nil {
		return &ValidationError{Errors: []api.FieldError{fieldErr("/stays_in_vra", "invalid",
			"A project that stays in vRA has no rvav project.")}}
	}
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
	if err := s.Store.SetVRAProjectMap(ctx, vraID, to, stay, p.User.ID); err != nil {
		return err
	}
	s.audit(ctx, p, "vra.project.map", "vra_project", vraID, nil, map[string]any{"rvav_project_id": to, "stays_in_vra": stay})
	return nil
}

// ImportVRA imports the estate read last: its templates as catalog items
// (drafts) with their sharing, and, when asked, its deployments adopted.
// It can run again: what was imported is linked, so only what changed is.
func (s *Service) ImportVRA(ctx context.Context, p *auth.Principal, req api.VraImportRequest) (api.VraImportResult, error) {
	out := api.VraImportResult{TemplatesCreated: []string{}, PropertyGroupsCreated: []string{}, FormLibraryCreated: []string{},
		ProjectsUpdated: []string{}, Created: []string{}, Updated: []string{}, Adopted: []string{}}
	out.Failed = []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}{}
	if !p.Everywhere(auth.ImportManage) {
		return out, ErrForbidden
	}
	// One import at a time, finished even if whoever started it leaves:
	// an item made and not yet linked would be in its template's way.
	ctx = context.WithoutCancel(ctx)
	unlock, ok, err := s.Store.TryLock(ctx, vraImportLock)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, &ConflictError{Msg: "Another import from vRA is running: wait for it, then look at the plan again."}
	}
	defer unlock()
	v, e, err := s.latestEstate(ctx)
	if err != nil {
		return out, err
	}
	// Only some vRA projects: their sharing and their deployments.
	only := map[string]bool{}
	if req.Projects != nil {
		for _, id := range *req.Projects {
			only[id] = true
		}
	}
	chosen := func(vraProjectID string) bool { return req.Projects == nil || only[vraProjectID] }
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
	shareTo := map[string]bool{} // rvav projects of the chosen vRA projects
	for _, pp := range plan.Projects {
		if pp.RvavProjectID != "" && chosen(pp.VRAID) {
			shareTo[pp.RvavProjectID] = true
		}
	}
	if req.Catalog == nil || *req.Catalog {
		if out.ProjectsUpdated, err = s.vraProjectProperties(ctx, p, plan.Projects, chosen); err != nil {
			return out, err
		}
		// Templates, property groups and form library entries first: items
		// name them.
		for _, g := range plan.PropertyGroups {
			if g.Action != vraimport.ActionCreate {
				continue
			}
			if err := s.vraPropertyGroup(ctx, p, g); isRefusal(err) {
				fail("Property group "+g.Key, err)
				continue
			} else if err != nil {
				return out, err
			}
			out.PropertyGroupsCreated = append(out.PropertyGroupsCreated, g.Key)
		}
		for _, li := range plan.FormLibrary {
			if li.Action != vraimport.ActionCreate {
				continue
			}
			if err := s.vraLibraryEntry(ctx, p, li); isRefusal(err) {
				fail("Form library entry "+li.Key, err)
				continue
			} else if err != nil {
				return out, err
			}
			out.FormLibraryCreated = append(out.FormLibraryCreated, li.Key)
		}
		for _, t := range plan.Templates {
			if t.Action != vraimport.ActionCreate {
				continue
			}
			if err := s.vraTemplate(ctx, p, t); isRefusal(err) {
				fail("Template "+t.Key, err)
				continue
			} else if err != nil {
				return out, err
			}
			out.TemplatesCreated = append(out.TemplatesCreated, t.Key)
		}
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
			// Sharing is only added, and each share recorded: one an
			// administrator removes later isn't made again.
			add := slices.DeleteFunc(slices.Clone(it.ShareAdd), func(id string) bool { return !shareTo[id] })
			if len(add) == 0 {
				continue
			}
			shares := make([]store.ProjectShare, len(add))
			for i, id := range add {
				shares[i].ProjectID = id
				if j := slices.IndexFunc(it.ShareAudiences, func(a vraimport.ShareAudience) bool { return a.ProjectID == id }); j >= 0 {
					a := it.ShareAudiences[j]
					shares[i].Audience = store.Audience{UserIDs: a.UserIDs, Groups: a.Groups, Roles: a.Roles}
				}
			}
			if err := s.Store.AddShares(ctx, itemID, shares); err != nil {
				return out, err
			}
			detail := map[string]any{"added": add, "by": "vra import"}
			if len(it.ShareAudiences) > 0 {
				detail["audiences"] = it.ShareAudiences
			}
			s.auditCatalog(ctx, p, "catalog.item.entitlements", itemID, detail)
			for _, id := range add {
				if err := s.Store.SaveVRALink(ctx, store.VRALink{Kind: "share", VRAID: it.BlueprintID, RvavID: itemID + "/" + id},
					p.User.ID); err != nil {
					return out, err
				}
			}
			out.Shared += len(add)
		}
	}
	if req.Deployments != nil && *req.Deployments {
		// Planned again: deployments find the items just imported.
		again, _, err := s.vraPlan(ctx, e, nil)
		if err != nil {
			return out, err
		}
		projectOf := map[string]string{}
		for _, d := range e.Deployments {
			projectOf[d.ID] = d.ProjectID
		}
		todo := slices.DeleteFunc(again.Deployments, func(d vraimport.DeploymentPlan) bool { return !chosen(projectOf[d.VRAID]) })
		adopted, err := s.adoptPlans(ctx, p, todo)
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
	s.audit(ctx, p, "vra.import", "vra_snapshot", v.ID, nil, map[string]any{"templates": out.TemplatesCreated,
		"property_groups": out.PropertyGroupsCreated, "form_library": out.FormLibraryCreated, "projects": out.ProjectsUpdated,
		"created": out.Created, "updated": out.Updated,
		"shared": out.Shared, "adopted": out.Adopted, "failed": len(out.Failed)})
	after, _, err := s.vraPlan(ctx, e, nil)
	if err != nil {
		return out, err
	}
	out.Plan, err = presentVRAPlan(v, after)
	return out, err
}

// vraProjectProperties gives the rvav projects of the chosen vRA projects
// the custom properties they lack (ADR-0038): added only, so what an
// administrator set in rvav stays.
func (s *Service) vraProjectProperties(ctx context.Context, p *auth.Principal, projects []vraimport.ProjectPlan,
	chosen func(string) bool) ([]string, error) {
	add := map[string]map[string]string{}
	var order []string
	for _, pp := range projects {
		if pp.RvavProjectID == "" || len(pp.PropertiesAdd) == 0 || !chosen(pp.VRAID) {
			continue
		}
		if add[pp.RvavProjectID] == nil {
			add[pp.RvavProjectID] = map[string]string{}
			order = append(order, pp.RvavProjectID)
		}
		for k, v := range pp.PropertiesAdd {
			if _, has := add[pp.RvavProjectID][k]; !has {
				add[pp.RvavProjectID][k] = v
			}
		}
	}
	updated := []string{}
	for _, id := range order {
		pr, err := s.Store.GetProject(ctx, id)
		if err != nil {
			return nil, err
		}
		props := maps.Clone(pr.Properties)
		if props == nil {
			props = map[string]string{}
		}
		added := map[string]string{}
		for k, v := range add[id] {
			if _, has := props[k]; !has && len(props) < blueprint.MaxProperties {
				props[k], added[k] = v, v
			}
		}
		if len(added) == 0 {
			continue
		}
		if err := s.Store.UpdateProject(ctx, id, store.ProjectUpdate{Properties: &props}); err != nil {
			return nil, err
		}
		s.audit(ctx, p, "project.update", "project", id, &id, map[string]any{"properties_added": added, "by": "vra import"})
		updated = append(updated, pr.Name)
	}
	return updated, nil
}

// VROTriageMarkdown is the vRO triage report of the estate read last.
func (s *Service) VROTriageMarkdown(ctx context.Context, p *auth.Principal) (string, error) {
	if !p.Everywhere(auth.ImportManage) {
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

// SetVRACutover marks a vRA project moved to rvav, or not: from then on
// new requests go to rvav, and the plan checks vRA offers it nothing
// (P2.6).
func (s *Service) SetVRACutover(ctx context.Context, p *auth.Principal, vraID string, w api.VraCutoverWrite) error {
	if !p.Everywhere(auth.ImportManage) {
		return ErrForbidden
	}
	_, e, err := s.latestEstate(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(e.Projects, func(x vra.Project) bool { return x.ID == vraID })
	if i < 0 {
		return ErrNotFound
	}
	if w.Moved {
		plan, _, err := s.vraPlan(ctx, e, nil)
		if err != nil {
			return err
		}
		if j := slices.IndexFunc(plan.Cutover.Projects, func(c vraimport.CutoverProject) bool { return c.VRAID == vraID }); j < 0 ||
			plan.Cutover.Projects[j].RvavProjectID == "" {
			return &ValidationError{Errors: []api.FieldError{fieldErr("/moved", "unmapped",
				e.Projects[i].Name+" has no rvav project: map it first.")}}
		}
	}
	note := strings.TrimSpace(deref(w.Note))
	if utf8.RuneCountInString(note) > 500 {
		return &ValidationError{Errors: []api.FieldError{fieldErr("/note", "too_long", "A note is at most 500 characters.")}}
	}
	if err := s.Store.SetVRAMoved(ctx, vraID, w.Moved, note, p.User.ID); err != nil {
		return err
	}
	s.audit(ctx, p, "vra.project.cutover", "vra_project", vraID, nil, map[string]any{"moved": w.Moved, "note": deref(w.Note)})
	return nil
}

// vraImportLock is the advisory lock one import holds.
const vraImportLock = 0x76726169 // "vrai"
