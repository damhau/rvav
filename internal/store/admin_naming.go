package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Naming policies (ADR-0020).

type NamingPolicy struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Template  string    `db:"template"`
	Counter   string    `db:"counter"` // prefix or policy
	CreatedAt time.Time `db:"created_at"`
}

func (s *Store) NamingPolicies(ctx context.Context) ([]NamingPolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select id, name, template, counter, created_at from naming_policies order by name, id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[NamingPolicy])
}

func (s *Store) GetNamingPolicy(ctx context.Context, id string) (NamingPolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select id, name, template, counter, created_at from naming_policies where id = $1`, id)
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[NamingPolicy])
	return p, notFound(err)
}

// SaveNamingPolicy creates or replaces a policy with its offers.
func (s *Store) SaveNamingPolicy(ctx context.Context, p NamingPolicy, offers []Offer) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into naming_policies (id, name, template, counter) values ($1, $2, $3, $4)
			on conflict (id) do update set name = excluded.name, template = excluded.template, counter = excluded.counter`,
			p.ID, p.Name, p.Template, p.Counter); err != nil {
			return err
		}
		return setOffers(ctx, tx, "naming", p.ID, offers)
	})
}

func (s *Store) DeleteNamingPolicy(ctx context.Context, id string) error {
	return deleteOffered(ctx, s, "naming", `delete from naming_policies where id = $1`, id)
}

// NamingPolicyFor is the policy offered to a project and environment, the
// most specific first (project and environment, project, environment,
// all); ErrNotFound when none is.
func (s *Store) NamingPolicyFor(ctx context.Context, projectID, environment string) (NamingPolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select p.id, p.name, p.template, p.counter, p.created_at
		from offers o join naming_policies p on p.id = o.key
		where o.kind = 'naming' and (o.project_id is null or o.project_id = $1) and (o.environment is null or o.environment = $2)
		order by (o.project_id is not null) desc, (o.environment is not null) desc, p.created_at, p.id limit 1`, projectID, environment)
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[NamingPolicy])
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// LockNamePrefix holds the names starting with a prefix until the
// transaction ends: two requests naming VMs from the same prefix take turns,
// counters of different policies included, so neither gives a name the other
// is giving. Take it before reading the names in use.
func (s *Store) LockNamePrefix(ctx context.Context, tx pgx.Tx, prefix string) error {
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('rvav.name:' || $1))`, prefix)
	return err
}

// DeploymentNames are the names of every deployment rvav made starting
// with the prefix, and of the VMs of its multi-VM deployments, deleted
// ones too: a name is never given twice.
func (s *Store) DeploymentNames(ctx context.Context, prefix string) (map[string]bool, error) {
	// A multi-VM deployment's VMs are named in its request's plan (ADR-0024).
	rows, _ := s.Pool.Query(ctx, `select name from deployments where starts_with(name, $1)
		union select v->>'name' from requests r, jsonb_array_elements(case when jsonb_typeof(r.plan->'components') = 'array'
			then r.plan->'vms' else '[]' end) v
		where starts_with(v->>'name', $1)`, prefix)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out, nil
}
