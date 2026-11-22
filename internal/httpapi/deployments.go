package httpapi

import (
	"errors"
	"maps"
	"net/http"
	"slices"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

func (s *Server) ListDeployments(w http.ResponseWriter, r *http.Request, params api.ListDeploymentsParams) {
	p := principal(r)
	after, err := cursorOf(params.Cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sc, err := s.Core.DeploymentScope(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f := store.DeploymentFilter{Projects: sc.Projects, OwnOnly: sc.OwnOnly, Viewer: sc.Viewer, ProjectID: deref(params.ProjectId),
		Search: deref(params.Q), LeaseExpiresBefore: params.LeaseExpiresBefore, Limit: limitOf(params.Limit), After: after, Sort: "name",
		Environments: derefAll(params.Environment), Items: derefAll(params.Item), Templates: derefAll(params.Template)}
	if params.Sort != nil {
		f.Sort = string(*params.Sort)
	}
	for _, st := range derefAll(params.Status) {
		switch st {
		case api.DeploymentStatusFilterExpired:
			f.Expired = true
		case api.DeploymentStatusFilterDeleted:
			f.IncludeDeleted = true
			fallthrough
		default:
			f.Statuses = append(f.Statuses, string(st))
		}
	}
	switch o := deref(params.Owner); o {
	case "":
	case "me":
		f.OwnerID = p.User.ID
	default:
		f.OwnerID = o
	}
	page, err := s.Store.ListDeployments(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.DeploymentPage{Items: make([]api.Deployment, len(page.Items)), NextCursor: nextCursor(page.Next)}
	for i, d := range page.Items {
		out.Items[i] = present.Deployment(d)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) GetDeploymentFacets(w http.ResponseWriter, r *http.Request, params api.GetDeploymentFacetsParams) {
	out := api.DeploymentFacets{Templates: []string{}, Environments: []api.EnvironmentKey{}}
	out.Items = []struct {
		Id   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}{}
	sc, err := s.Core.DeploymentScope(r.Context(), principal(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f, err := s.Store.DeploymentFacets(r.Context(), sc, deref(params.ProjectId))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, i := range f.Items {
		out.Items = append(out.Items, struct {
			Id   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		}{Id: i.ID, Name: i.Name, Slug: i.Slug})
	}
	out.Templates, out.Environments = f.Templates, f.Environments
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ListVms(w http.ResponseWriter, r *http.Request, params api.ListVmsParams) {
	p := principal(r)
	after, err := cursorOf(params.Cursor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sc, err := s.Core.DeploymentScope(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f := store.VMFilter{Projects: sc.Projects, OwnOnly: sc.OwnOnly, Viewer: sc.Viewer, ProjectID: deref(params.ProjectId),
		Environments: derefAll(params.Environment), VCenter: deref(params.Vcenter), Cluster: deref(params.Cluster),
		Network: deref(params.Network), Search: deref(params.Q), Limit: limitOf(params.Limit), After: after}
	if params.Sort != nil {
		f.Sort = string(*params.Sort)
	}
	for _, st := range derefAll(params.PowerState) {
		f.PowerStates = append(f.PowerStates, string(st))
	}
	out := api.VmPage{Items: []api.VmListItem{}}
	page, err := s.Store.ListVMs(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	links, err := s.Store.VCenterLinks(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, v := range page.Items {
		if item, ok := present.VMListItem(v, links); ok {
			out.Items = append(out.Items, item)
		}
	}
	out.NextCursor = nextCursor(page.Next)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) GetVmFacets(w http.ResponseWriter, r *http.Request, params api.GetVmFacetsParams) {
	out := api.VmFacets{Networks: []string{}}
	out.Vcenters = []struct {
		Clusters []string `json:"clusters"`
		Name     string   `json:"name"`
	}{}
	sc, err := s.Core.DeploymentScope(r.Context(), principal(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f, err := s.Store.VMFacets(r.Context(), sc, deref(params.ProjectId))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, name := range slices.Sorted(maps.Keys(f.Clusters)) {
		out.Vcenters = append(out.Vcenters, struct {
			Clusters []string `json:"clusters"`
			Name     string   `json:"name"`
		}{Clusters: f.Clusters[name], Name: name})
	}
	out.Networks = f.Networks
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ListSavedViews(w http.ResponseWriter, r *http.Request, params api.ListSavedViewsParams) {
	items, err := s.Core.SavedViews(r.Context(), principal(r), params.List)
	s.reply(w, r, http.StatusOK, api.SavedViewList{Items: items}, err)
}

func (s *Server) CreateSavedView(w http.ResponseWriter, r *http.Request) {
	var b api.SavedViewWrite
	if !s.decode(w, r, &b) {
		return
	}
	v, err := s.Core.SaveView(r.Context(), principal(r), "", b)
	s.reply(w, r, http.StatusCreated, v, err)
}

func (s *Server) UpdateSavedView(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	var b api.SavedViewWrite
	if !s.decode(w, r, &b) {
		return
	}
	v, err := s.Core.SaveView(r.Context(), principal(r), id, b)
	s.reply(w, r, http.StatusOK, v, err)
}

func (s *Server) DeleteSavedView(w http.ResponseWriter, r *http.Request, id api.IdPath) {
	s.replyEmpty(w, r, s.Core.DeleteSavedView(r.Context(), principal(r), id))
}

func (s *Server) GetDeployment(w http.ResponseWriter, r *http.Request, deploymentID string) {
	d, err := s.Store.GetDeployment(r.Context(), deploymentID)
	if err == nil && !core.SeesDeployment(principal(r), d.ProjectID, d.ProjectSharing, d.OwnerID) {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	resources, err := s.Store.ListResources(r.Context(), d.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	links, err := s.Store.VCenterLinks(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := present.DeploymentDetail(d, resources, s.secretFields(r, d.VersionID), links)
	key, err := s.Store.DeploymentKey(r.Context(), s.Store.Pool, d.ID)
	switch {
	case err == nil:
		out.GeneratedKey = present.GeneratedKey(key)
	case !errors.Is(err, store.ErrNotFound):
		s.fail(w, r, err)
		return
	}
	ref, err := s.Core.ServiceNowRefOfDeployment(r.Context(), d.ID)
	out.Servicenow = ref
	s.reply(w, r, http.StatusOK, out, err)
}

// DownloadPrivateKey hands the deployment's owner its generated private
// key, once (ADR-0039).
func (s *Server) DownloadPrivateKey(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath) {
	k, err := s.Core.DownloadPrivateKey(r.Context(), principal(r), deploymentID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.PrivateKey{PrivateKey: k.PEM, Username: k.Username, Fingerprint: k.Fingerprint})
}
