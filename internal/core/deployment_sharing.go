package core

import (
	"context"
	"errors"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// SeesDeployment says whether p sees a deployment of the project, by the
// project's deployment sharing (ADR-0045): its owner always; whoever sees
// the project when every member sees its deployments; otherwise only who
// holds deployments.read_all there.
func SeesDeployment(p *auth.Principal, projectID, sharing, ownerID string) bool {
	if ownerID == p.User.ID {
		return true
	}
	return p.In(auth.ProjectSee, projectID) && (sharing != store.SharingOwnerOnly || p.In(auth.DeploymentsReadAll, projectID))
}

// DeploymentScope is what p sees of every project's deployments.
func (s *Service) DeploymentScope(ctx context.Context, p *auth.Principal) (store.Scope, error) {
	sc := store.Scope{Projects: p.Projects(auth.ProjectSee), Viewer: p.User.ID}
	if p.Everywhere(auth.DeploymentsReadAll) {
		return sc, nil
	}
	own, err := s.Store.OwnerOnlyProjects(ctx)
	if err != nil {
		return sc, err
	}
	for _, id := range own {
		if !p.In(auth.DeploymentsReadAll, id) {
			sc.OwnOnly = append(sc.OwnOnly, id)
		}
	}
	return sc, nil
}

// SetDeploymentSharing sets who sees the project's deployments, for whoever
// manages its members.
func (s *Service) SetDeploymentSharing(ctx context.Context, p *auth.Principal, projectID string, w api.DeploymentSharingWrite) (api.Project, error) {
	proj, err := s.projectFor(ctx, p, auth.ProjectMembersManage, projectID)
	if err != nil {
		return api.Project{}, err
	}
	sharing := string(w.DeploymentSharing)
	if !w.DeploymentSharing.Valid() {
		return api.Project{}, &ValidationError{Errors: []api.FieldError{fieldErr("/deployment_sharing", "invalid",
			"Choose all_members or owner_only.")}}
	}
	if sharing != proj.DeploymentSharing {
		if err := s.Store.SetDeploymentSharing(ctx, proj.ID, sharing); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return api.Project{}, ErrNotFound
			}
			return api.Project{}, err
		}
		s.audit(ctx, p, "project.sharing.set", "project", proj.ID, &proj.ID,
			map[string]any{"from": proj.DeploymentSharing, "to": sharing})
		if s.Events != nil {
			// Streams opened before hold what the project's members saw.
			s.Events.AccessChanged(ctx)
		}
		proj.DeploymentSharing = sharing
	}
	return present.Project(proj, p.PermissionsIn(proj.ID)), nil
}
