package store

import "context"

// Deployment sharing (ADR-0045).

const (
	SharingAllMembers = "all_members"
	SharingOwnerOnly  = "owner_only"
	// EnforcementSoft policies warn instead of denying or asking for
	// approval (ADR-0045).
	EnforcementSoft = "soft"
	EnforcementHard = "hard"
)

// OwnerOnlyProjects lists the projects whose deployments show to their
// owners only.
func (s *Store) OwnerOnlyProjects(ctx context.Context) ([]string, error) {
	return s.strings(ctx, `select id from projects where deployment_sharing = $1 order by id`, SharingOwnerOnly)
}

// ProjectIDs lists every project.
func (s *Store) ProjectIDs(ctx context.Context) ([]string, error) {
	return s.strings(ctx, `select id from projects order by id`)
}

// SetDeploymentSharing sets who sees a project's deployments.
func (s *Store) SetDeploymentSharing(ctx context.Context, db DB, projectID, sharing string) error {
	tag, err := db.Exec(ctx, `update projects set deployment_sharing = $2 where id = $1`, projectID, sharing)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeploymentOwner is a deployment's owner, for events about its resources.
func (s *Store) DeploymentOwner(ctx context.Context, id string) (string, error) {
	var owner string
	err := s.Pool.QueryRow(ctx, `select owner_id from deployments where id = $1`, id).Scan(&owner)
	return owner, notFound(err)
}
