package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Custom resource types (ADR-0056).

// ResourceDriver is what runs one step of a custom resource's life: a
// custom workflow (kind workflow, by key) or an AWX job template (kind awx,
// by name).
type ResourceDriver struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// ResourceLifecycle is a type's drivers by step; update and read are optional.
type ResourceLifecycle struct {
	Create ResourceDriver  `json:"create"`
	Delete ResourceDriver  `json:"delete"`
	Update *ResourceDriver `json:"update,omitempty"`
	Read   *ResourceDriver `json:"read,omitempty"`
}

// Driver is the step's driver, nil when the type has none.
func (l ResourceLifecycle) Driver(phase string) *ResourceDriver {
	switch phase {
	case "create":
		return &l.Create
	case "delete":
		return &l.Delete
	case "update":
		return l.Update
	case "read":
		return l.Read
	}
	return nil
}

// ResourceType is a custom resource type.
type ResourceType struct {
	Key            string            `db:"key"`
	Title          string            `db:"title"`
	Description    string            `db:"description"`
	Lifecycle      ResourceLifecycle `db:"lifecycle"`
	PropertySchema json.RawMessage   `db:"property_schema"`
	Actions        []string          `db:"actions"`
	Enabled        bool              `db:"enabled"`
	CreatedAt      time.Time         `db:"created_at"`
	UpdatedAt      time.Time         `db:"updated_at"`
}

const resourceTypeCols = `key, title, description, lifecycle, property_schema, actions, enabled, created_at, updated_at`

func (s *Store) ResourceTypes(ctx context.Context) ([]ResourceType, error) {
	rows, _ := s.Pool.Query(ctx, `select `+resourceTypeCols+` from resource_types order by key`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ResourceType])
}

func (s *Store) GetResourceType(ctx context.Context, key string) (ResourceType, error) {
	rows, _ := s.Pool.Query(ctx, `select `+resourceTypeCols+` from resource_types where key = $1`, key)
	t, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ResourceType])
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// SaveResourceType inserts or replaces one.
func (s *Store) SaveResourceType(ctx context.Context, t ResourceType) error {
	_, err := s.Pool.Exec(ctx, `insert into resource_types (key, title, description, lifecycle, property_schema, actions, enabled)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (key) do update set title = excluded.title, description = excluded.description,
			lifecycle = excluded.lifecycle, property_schema = excluded.property_schema, actions = excluded.actions,
			enabled = excluded.enabled, updated_at = now()`,
		t.Key, t.Title, t.Description, t.Lifecycle, jsonOr(t.PropertySchema, `{"type": "object"}`), nonNil(t.Actions), t.Enabled)
	return err
}

func (s *Store) DeleteResourceType(ctx context.Context, key string) error {
	tag, err := s.Pool.Exec(ctx, `delete from resource_types where key = $1`, key)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ResourceTypeUse is what uses a type: the items whose published or draft
// versions name it, and how many resources of it deployments have.
type ResourceTypeUse struct {
	Resources int
	UsedBy    []string
}

// ResourceTypeUses says, for each type, what uses it.
func (s *Store) ResourceTypeUses(ctx context.Context) (map[string]ResourceTypeUse, error) {
	out := map[string]ResourceTypeUse{}
	rows, err := s.Pool.Query(ctx, `select t.key,
			(select count(*) from resources r join deployments d on d.id = r.deployment_id
				where r.type = 'custom' and r.properties->'resource_type'->>'key' = t.key and d.status <> 'deleted'),
			array(select distinct 'item ' || i.slug from catalog_item_versions v join catalog_items i on i.id = v.item_id
				where v.state in ('draft', 'published') and (
					jsonb_path_exists(v.blueprint, '$.resources[*].type ? (@ == $k)', jsonb_build_object('k', t.key))
					or jsonb_path_exists(v.blueprint, '$.**.resource.type ? (@ == $k)', jsonb_build_object('k', t.key))))
		from resource_types t`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var u ResourceTypeUse
		if err := rows.Scan(&key, &u.Resources, &u.UsedBy); err != nil {
			return nil, err
		}
		out[key] = u
	}
	return out, rows.Err()
}

// CustomResourceType is a custom resource's type, as the resource keeps it.
type CustomResourceType struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// CustomProps are a custom resource's properties column: its type, its key
// in the blueprint (or composite step), what made it (blueprint or
// composite), what deletes it (rvav or awx, for the timeline), the inputs
// its create or last update was given, the properties it returned, and
// their titles from the type's schema.
type CustomProps struct {
	ResourceType CustomResourceType `json:"resource_type"`
	Key          string             `json:"key"`
	Source       string             `json:"source,omitempty"`
	System       string             `json:"system,omitempty"`
	Inputs       map[string]any     `json:"inputs,omitempty"`
	Properties   map[string]any     `json:"properties"`
	Titles       map[string]string  `json:"property_titles,omitempty"`
	// Creating is set while its create runs, and stays when create failed:
	// what it made, if anything, is deleted by the type's delete then.
	Creating bool `json:"creating,omitempty"`
}
