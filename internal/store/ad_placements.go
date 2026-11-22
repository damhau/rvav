package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ADPlacement says where the computer objects of a project's VMs, an
// environment's, both or all go in Active Directory (ADR-0064).
type ADPlacement struct {
	ID               string    `db:"id"`
	ProjectID        *string   `db:"project_id"`
	Environment      *string   `db:"environment"`
	OU               string    `db:"ou"`
	Groups           []string  `db:"groups"`
	AllowBlueprintOU bool      `db:"allow_blueprint_ou"`
	UpdatedAt        time.Time `db:"updated_at"`
	ProjectName      *string   `db:"project_name"`
}

const adPlacementSelect = `select a.id, a.project_id, a.environment, a.ou, a.groups, a.allow_blueprint_ou, a.updated_at,
	p.name as project_name from ad_placements a left join projects p on p.id = a.project_id`

// ADPlacements lists the rows that may apply to a project (its own and
// those of any project), or every row for "".
func (s *Store) ADPlacements(ctx context.Context, projectID string) ([]ADPlacement, error) {
	rows, _ := s.Pool.Query(ctx, adPlacementSelect+` where $1 = '' or a.project_id = $1 or a.project_id is null
		order by a.project_id nulls last, p.name, a.environment nulls last, a.id`, projectID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ADPlacement])
}

func (s *Store) GetADPlacement(ctx context.Context, id string) (ADPlacement, error) {
	rows, _ := s.Pool.Query(ctx, adPlacementSelect+` where a.id = $1`, id)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ADPlacement])
	return r, notFound(err)
}

// ADPlacementFor is the row that applies to a VM of a project in an
// environment: the most specific one.
func (s *Store) ADPlacementFor(ctx context.Context, projectID, environment string) (*ADPlacement, error) {
	rows, _ := s.Pool.Query(ctx, adPlacementSelect+` where (a.project_id = $1 or a.project_id is null)
		and (a.environment = $2 or a.environment is null)
		order by a.project_id is null, a.environment is null limit 1`, projectID, environment)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ADPlacement])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

// SaveADPlacement creates the row or replaces it; another row for the same
// project and environment is a unique violation.
func (s *Store) SaveADPlacement(ctx context.Context, r ADPlacement) error {
	_, err := s.Pool.Exec(ctx, `insert into ad_placements (id, project_id, environment, ou, groups, allow_blueprint_ou)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set project_id = excluded.project_id, environment = excluded.environment, ou = excluded.ou,
		groups = excluded.groups, allow_blueprint_ou = excluded.allow_blueprint_ou, updated_at = now()`,
		r.ID, r.ProjectID, r.Environment, r.OU, nonNil(r.Groups), r.AllowBlueprintOU)
	return err
}

func (s *Store) DeleteADPlacement(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from ad_placements where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
