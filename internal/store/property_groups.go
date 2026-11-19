package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// PropertyGroup is one version of a property group (ADR-0038): Inputs is a
// JSON list of GroupInput, Constants a JSON object.
type PropertyGroup struct {
	Key         string          `db:"key"`
	Version     int             `db:"version"`
	Name        string          `db:"name"`
	Description string          `db:"description"`
	Inputs      json.RawMessage `db:"inputs"`
	Constants   json.RawMessage `db:"constants"`
	CreatedAt   time.Time       `db:"created_at"`
}

// GroupInput is an input a property group adds to the forms of the items
// naming it, as the API has it.
type GroupInput struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Secret   bool     `json:"secret,omitempty"`
	Default  any      `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Help     string   `json:"help,omitempty"`
	// ExposeToInventory puts it in the AWX inventory's host variables, as
	// a form field's expose_to_inventory does; never a secret one.
	ExposeToInventory bool `json:"expose_to_inventory,omitempty"`
}

// GroupRef is a blueprint's reference to a version of a property group, in
// its property_groups.
type GroupRef struct {
	Key     string `json:"key"`
	Version int    `json:"version"`
}

// GroupRefs reads the property groups a blueprint names; none when they
// aren't written as GroupRefs (saving says why).
func GroupRefs(blueprint json.RawMessage) []GroupRef {
	var b struct {
		PropertyGroups []GroupRef `json:"property_groups"`
	}
	if json.Unmarshal(blueprint, &b) != nil {
		return nil
	}
	return b.PropertyGroups
}

const groupCols = `key, version, name, description, inputs, constants, created_at`

// PropertyGroups lists every property group at its latest version.
func (s *Store) PropertyGroups(ctx context.Context) ([]PropertyGroup, error) {
	rows, _ := s.Pool.Query(ctx, `select distinct on (key) `+groupCols+` from property_groups order by key, version desc`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[PropertyGroup])
}

// GetPropertyGroup gives a property group at a version, at its latest when
// version is 0.
func (s *Store) GetPropertyGroup(ctx context.Context, key string, version int) (PropertyGroup, error) {
	rows, _ := s.Pool.Query(ctx, `select `+groupCols+` from property_groups where key = $1 and ($2 = 0 or version = $2)
		order by version desc limit 1`, key, version)
	g, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[PropertyGroup])
	return g, notFound(err)
}

// PropertyGroupVersions lists a property group's version numbers, the
// latest last.
func (s *Store) PropertyGroupVersions(ctx context.Context, key string) ([]int, error) {
	rows, _ := s.Pool.Query(ctx, `select version from property_groups where key = $1 order by version`, key)
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

// AddPropertyGroupVersion saves a property group as its next version, its
// first when first, and gives its number: ErrInUse when first and the key
// exists, or when another save took the number; ErrNotFound when not first
// and it doesn't.
func (s *Store) AddPropertyGroupVersion(ctx context.Context, g PropertyGroup, first bool, by string) (int, error) {
	var version int
	err := s.Pool.QueryRow(ctx, `insert into property_groups (key, version, name, description, inputs, constants, created_by)
		select $1, coalesce(max(version), 0) + 1, $2, $3, $4, $5, nullif($6, '') from property_groups where key = $1
		having (count(*) = 0) = $7 returning version`,
		g.Key, g.Name, g.Description, g.Inputs, g.Constants, by, first).Scan(&version)
	switch {
	case IsUniqueViolation(err), errors.Is(err, pgx.ErrNoRows) && first:
		return 0, ErrInUse
	case errors.Is(err, pgx.ErrNoRows):
		return 0, ErrNotFound
	}
	return version, err
}

// DeletePropertyGroup deletes every version of a property group, unless
// an item's version other than a draft names it: it gives their slugs
// then, and deletes nothing.
func (s *Store) DeletePropertyGroup(ctx context.Context, key string) ([]string, error) {
	var users []string
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if err := LockPropertyGroup(ctx, tx, key); err != nil {
			return err
		}
		var err error
		if users, err = groupUsers(ctx, tx, key); err != nil || len(users) > 0 {
			return err
		}
		tag, err := tx.Exec(ctx, `delete from property_groups where key = $1`, key)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
	return users, err
}

// LockPropertyGroup holds a property group until the transaction ends, so
// that it isn't deleted while a version naming it is published.
func LockPropertyGroup(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('rvav.property_group:' || $1))`, key)
	return err
}

// PropertyGroupUsers lists the slugs of the items with a version other
// than a draft whose blueprint names the property group.
func (s *Store) PropertyGroupUsers(ctx context.Context, key string) ([]string, error) {
	return groupUsers(ctx, s.Pool, key)
}

func groupUsers(ctx context.Context, db DB, key string) ([]string, error) {
	rows, _ := db.Query(ctx, `select distinct i.slug from catalog_item_versions v join catalog_items i on i.id = v.item_id
		where v.state <> 'draft' and jsonb_typeof(v.blueprint->'property_groups') = 'array'
			and v.blueprint->'property_groups' @> jsonb_build_array(jsonb_build_object('key', $1::text))
		order by i.slug`, key)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// GetVersion gives a version as requesters get it: the inputs of the
// property groups its blueprint names are in its form, their constants in
// GroupConstants (ADR-0038). GetVersionAsWritten gives it as saved.
func (s *Store) GetVersion(ctx context.Context, id string) (ItemVersion, error) {
	v, err := s.GetVersionAsWritten(ctx, id)
	refs := GroupRefs(v.Blueprint)
	if err != nil || len(refs) == 0 {
		return v, err
	}
	groups, err := groupsOf(ctx, s.Pool, refs)
	if err != nil {
		return v, err
	}
	return WithGroups(v, groups), nil
}

// LockVersionPublished holds the version as it is until tx ends, so that
// it isn't retired meanwhile, and says whether it is published.
func (s *Store) LockVersionPublished(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var state string
	err := tx.QueryRow(ctx, `select state from catalog_item_versions where id = $1 for share`, id).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return state == "published", err
}

// UISchemaWithGroups is a version's UI schema with the fields of the
// property groups its blueprint names, as GetVersion gives it: what tells
// a group's secret inputs, and those exposed to the AWX inventory.
func UISchemaWithGroups(ctx context.Context, db DB, uiSchema, blueprint json.RawMessage) (json.RawMessage, error) {
	refs := GroupRefs(blueprint)
	if len(refs) == 0 {
		return uiSchema, nil
	}
	groups, err := groupsOf(ctx, db, refs)
	if err != nil {
		return uiSchema, err
	}
	return WithGroups(ItemVersion{UISchema: uiSchema}, groups).UISchema, nil
}

func groupsOf(ctx context.Context, db DB, refs []GroupRef) ([]PropertyGroup, error) {
	groups := make([]PropertyGroup, 0, len(refs))
	for _, r := range refs {
		if r.Version < 1 {
			continue
		}
		rows, _ := db.Query(ctx, `select `+groupCols+` from property_groups where key = $1 and version = $2`, r.Key, r.Version)
		g, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[PropertyGroup])
		// Only a draft can name a group that's gone: saving it says so.
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// WithGroups is the version with the inputs of these property groups in
// its form, after its own, a section for each group's; an input the form
// has already keeps the form's.
func WithGroups(v ItemVersion, groups []PropertyGroup) ItemVersion {
	form := object(v.FormSchema)
	props := objectIn(form, "properties")
	required, _ := form["required"].([]any)
	ui := object(v.UISchema)
	fields := objectIn(ui, "fields")
	layout := objectIn(ui, "layout")
	if layout["kind"] == nil {
		layout["kind"] = "page"
	}
	sections, _ := layout["sections"].([]any)
	initial := object(v.InitialValues)
	consts := map[string]any{}
	for _, g := range groups {
		c := object(g.Constants)
		consts[g.Key] = c
		var inputs []GroupInput
		_ = json.Unmarshal(g.Inputs, &inputs)
		var names []any
		for _, in := range inputs {
			if _, ok := props[in.Name]; ok {
				continue
			}
			props[in.Name] = in.schema()
			fields[in.Name] = in.uiField()
			if in.Required {
				required = append(required, in.Name)
			}
			if _, ok := initial[in.Name]; !ok && in.Default != nil {
				initial[in.Name] = in.Default
			}
			names = append(names, in.Name)
		}
		if len(names) > 0 {
			sections = append(sections, map[string]any{"key": "propgroup-" + g.Key, "title": g.Name, "fields": names})
		}
	}
	if required != nil {
		form["required"] = required
	}
	layout["sections"] = sections
	if sections == nil {
		layout["sections"] = []any{}
	}
	v.FormSchema, _ = json.Marshal(form)
	v.UISchema, _ = json.Marshal(ui)
	v.InitialValues, _ = json.Marshal(initial)
	v.GroupConstants = consts
	return v
}

func (in GroupInput) schema() map[string]any {
	s := map[string]any{"type": in.Type}
	if len(in.Options) > 0 {
		s["enum"] = in.Options
	}
	if in.Type == "string" && in.Required {
		s["minLength"] = 1
	}
	return s
}

func (in GroupInput) uiField() map[string]any {
	f := map[string]any{"label": in.Label, "widget": "text"}
	switch {
	case in.Secret:
		f["widget"], f["secret"] = "secret", true
	case len(in.Options) > 0:
		opts := make([]any, len(in.Options))
		for i, o := range in.Options {
			opts[i] = map[string]any{"value": o, "label": o}
		}
		f["widget"], f["options"] = "select", opts
	case in.Type == "boolean":
		f["widget"] = "toggle"
	case in.Type == "integer" || in.Type == "number":
		f["widget"] = "number"
	}
	if in.Help != "" {
		f["help"] = in.Help
	}
	if in.ExposeToInventory && !in.Secret {
		f["expose_to_inventory"] = true
	}
	return f
}

func object(raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func objectIn(m map[string]any, key string) map[string]any {
	o, ok := m[key].(map[string]any)
	if !ok {
		o = map[string]any{}
		m[key] = o
	}
	return o
}
