package httpapi

import (
	"net/http"

	"github.com/damhau/rvav/internal/api"
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
	f := store.DeploymentFilter{Projects: p.VisibleProjects(), ProjectID: deref(params.ProjectId), Search: deref(params.Q),
		LeaseExpiresBefore: params.LeaseExpiresBefore, Limit: limitOf(params.Limit), After: after, Sort: "name"}
	if f.Projects != nil && len(f.Projects) == 0 {
		writeJSON(w, http.StatusOK, api.DeploymentPage{Items: []api.Deployment{}})
		return
	}
	if params.Sort != nil {
		f.Sort = string(*params.Sort)
	}
	if params.Status != nil {
		for _, st := range *params.Status {
			f.Statuses = append(f.Statuses, string(st))
			if st == api.DeploymentStatusDeleted {
				f.IncludeDeleted = true
			}
		}
	}
	if params.Environment != nil {
		f.Environments = *params.Environment
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

func (s *Server) GetDeployment(w http.ResponseWriter, r *http.Request, deploymentID string) {
	d, err := s.Store.GetDeployment(r.Context(), deploymentID)
	if err == nil && !principal(r).CanSeeProject(d.ProjectID) && d.OwnerID != principal(r).User.ID {
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
	writeJSON(w, http.StatusOK, present.DeploymentDetail(d, resources, s.secretFields(r, d.VersionID)))
}
