package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Catalog administration (ADR-0019): items in any state, their versions,
// and the version life cycle.

var (
	// ErrDraftExists: an item has at most one draft.
	ErrDraftExists = errors.New("the item already has a draft")
	// ErrVersionState: the version is not in a state that allows this.
	ErrVersionState = errors.New("not allowed in the version's state")
	// ErrInUse: something still refers to it.
	ErrInUse = errors.New("in use")
)

// AdminItem is a catalog item as catalog admins see it.
type AdminItem struct {
	ID                 string    `db:"id"`
	Slug               string    `db:"slug"`
	Name               string    `db:"name"`
	Summary            string    `db:"summary"`
	DescriptionMD      string    `db:"description_md"`
	Category           string    `db:"category"`
	Type               string    `db:"type"`
	Icon               string    `db:"icon"`
	Tags               []string  `db:"tags"`
	Featured           bool      `db:"featured"`
	SourceKind         string    `db:"source_kind"`
	AWXTemplateID      *int      `db:"awx_template_id"`
	AWXTemplateName    *string   `db:"awx_template_name"`
	PublishedVersionID *string   `db:"published_version_id"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
	AllProjects        bool      `db:"all_projects"`
	Requested          bool      `db:"requested"`
}

const adminItemSelect = `select i.id, i.slug, i.name, i.summary, i.description_md, i.category, i.type, i.icon, i.tags,
	i.featured, i.source_kind, i.awx_template_id, i.awx_template_name, i.published_version_id, i.created_at, i.updated_at,
	i.all_projects, exists (select 1 from requests r where r.item_id = i.id) as requested
	from catalog_items i`

type AdminItemFilter struct {
	Search, Category, Source string
	// State is "published", "unpublished" or "" (both).
	State string
	Limit int
	After *Cursor
}

func (s *Store) AdminItems(ctx context.Context, f AdminItemFilter) (Page[AdminItem], error) {
	after := Cursor{}
	if f.After != nil {
		after = *f.After
	}
	rows, _ := s.Pool.Query(ctx, adminItemSelect+`
		where ($1 = '' or i.name ilike $1 or i.slug ilike $1 or i.summary ilike $1 or array_to_string(i.tags, ' ') ilike $1)
		  and ($2 = '' or i.category = $2)
		  and ($3 = '' or i.source_kind = $3)
		  and ($4 = '' or ($4 = 'published') = (i.published_version_id is not null))
		  and ($5 = '' or (i.name, i.id) > ($5, $6))
		order by i.name, i.id limit $7`,
		SearchText(f.Search), f.Category, f.Source, f.State, after.Value, after.ID, f.Limit+1)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[AdminItem])
	if err != nil {
		return Page[AdminItem]{}, err
	}
	return paginate(list, f.Limit, func(i AdminItem) Cursor { return Cursor{Value: i.Name, ID: i.ID} }), nil
}

// GetAdminItem finds an item by ID or slug, in any state.
func (s *Store) GetAdminItem(ctx context.Context, idOrSlug string) (AdminItem, error) {
	rows, _ := s.Pool.Query(ctx, adminItemSelect+` where i.id = $1 or i.slug = $1`, idOrSlug)
	i, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[AdminItem])
	return i, notFound(err)
}

// VersionRow is a version without its definition.
type VersionRow struct {
	ID            string     `db:"id"`
	ItemID        string     `db:"item_id"`
	Number        string     `db:"number"`
	State         string     `db:"state"`
	Changelog     string     `db:"changelog"`
	CreatedAt     time.Time  `db:"created_at"`
	CreatedBy     *string    `db:"created_by"`
	CreatedByName *string    `db:"created_by_name"`
	PublishedAt   *time.Time `db:"published_at"`
	DeprecatedAt  *time.Time `db:"deprecated_at"`
	Deployments   int        `db:"deployments"`
}

const versionRowSelect = `select v.id, v.item_id, v.number, v.state, v.changelog, v.created_at, v.created_by,
	u.name as created_by_name, v.published_at, v.deprecated_at,
	(select count(*) from deployments d where d.version_id = v.id and d.status <> 'deleted')::int as deployments
	from catalog_item_versions v left join users u on u.id = v.created_by`

// ItemVersions lists an item's versions, newest first.
func (s *Store) ItemVersions(ctx context.Context, itemID string) ([]VersionRow, error) {
	rows, _ := s.Pool.Query(ctx, versionRowSelect+` where v.item_id = $1 order by v.created_at desc, v.id desc`, itemID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[VersionRow])
}

func (s *Store) GetVersionRow(ctx context.Context, id string) (VersionRow, error) {
	rows, _ := s.Pool.Query(ctx, versionRowSelect+` where v.id = $1`, id)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[VersionRow])
	return v, notFound(err)
}

// ItemProjects lists the projects an item is open to.
func (s *Store) ItemProjects(ctx context.Context, itemID string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select project_id from catalog_entitlements where item_id = $1 order by project_id`, itemID)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// VersionDef is a version's definition.
type VersionDef struct {
	FormSchema, UISchema, InitialValues, Blueprint json.RawMessage
}

// NewItem is an item to create, with its first draft.
type NewItem struct {
	ID, Slug, Name, Summary, DescriptionMD, Category, Type, Icon string
	Tags                                                         []string
	Featured                                                     bool
}

// NewVersion is a draft to create.
type NewVersion struct {
	ID, ItemID, Number, Changelog, CreatedBy string
	Def                                      VersionDef
}

// CreateItem creates an item and its first draft. A slug already used is
// ErrInUse.
func (s *Store) CreateItem(ctx context.Context, it NewItem, v NewVersion) error {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into catalog_items (id, slug, name, summary, description_md, category, type, icon, tags,
			featured) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			it.ID, it.Slug, it.Name, it.Summary, it.DescriptionMD, it.Category, it.Type, it.Icon, nonNilStrings(it.Tags), it.Featured); err != nil {
			return err
		}
		v.ItemID = it.ID
		return insertDraft(ctx, tx, v)
	})
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

func insertDraft(ctx context.Context, tx pgx.Tx, v NewVersion) error {
	_, err := tx.Exec(ctx, `insert into catalog_item_versions (id, item_id, number, state, form_schema, ui_schema,
		initial_values, blueprint, changelog, created_by) values ($1, $2, $3, 'draft', $4, $5, $6, $7, $8, $9)`,
		v.ID, v.ItemID, v.Number, v.Def.FormSchema, v.Def.UISchema, jsonOr(v.Def.InitialValues, "{}"), jsonOr(v.Def.Blueprint, "{}"),
		v.Changelog, v.CreatedBy)
	return err
}

// CreateDraft adds a draft to an item (ErrDraftExists when it has one).
func (s *Store) CreateDraft(ctx context.Context, v NewVersion) error {
	err := s.Tx(ctx, func(tx pgx.Tx) error { return insertDraft(ctx, tx, v) })
	if IsUniqueViolation(err) {
		return ErrDraftExists
	}
	return err
}

// ItemUpdate changes an item's metadata; nil fields stay as they are.
type ItemUpdate struct {
	Slug, Name, Summary, DescriptionMD, Category, Icon *string
	Tags                                               *[]string
	Featured                                           *bool
}

func (s *Store) UpdateItem(ctx context.Context, id string, u ItemUpdate) error {
	var tags []string
	if u.Tags != nil {
		tags = nonNilStrings(*u.Tags)
	}
	tag, err := s.Pool.Exec(ctx, `update catalog_items set slug = coalesce($2, slug), name = coalesce($3, name),
		summary = coalesce($4, summary), description_md = coalesce($5, description_md), category = coalesce($6, category),
		icon = coalesce($7, icon), tags = coalesce($8, tags), featured = coalesce($9, featured), updated_at = now()
		where id = $1`, id, u.Slug, u.Name, u.Summary, u.DescriptionMD, u.Category, u.Icon, tags, u.Featured)
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteItem deletes an item no request was made from (ErrInUse otherwise).
func (s *Store) DeleteItem(ctx context.Context, id string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var requested bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from requests where item_id = $1)`, id).Scan(&requested); err != nil {
			return err
		}
		if requested {
			return ErrInUse
		}
		if _, err := tx.Exec(ctx, `update catalog_items set published_version_id = null where id = $1`, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `delete from catalog_items where id = $1`, id)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// SaveDraft replaces a draft's definition, and its number and changelog
// when given (ErrVersionState when the version is not a draft).
func (s *Store) SaveDraft(ctx context.Context, versionID string, number, changelog *string, def VersionDef) error {
	tag, err := s.Pool.Exec(ctx, `update catalog_item_versions set form_schema = $2, ui_schema = $3, initial_values = $4,
		blueprint = $5, number = coalesce($6, number), changelog = coalesce($7, changelog)
		where id = $1 and state = 'draft'`, versionID, def.FormSchema, def.UISchema, jsonOr(def.InitialValues, "{}"),
		jsonOr(def.Blueprint, "{}"), number, changelog)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrVersionState
	}
	return err
}

// DeleteDraft discards a draft (ErrVersionState when it is not one).
func (s *Store) DeleteDraft(ctx context.Context, versionID string) error {
	tag, err := s.Pool.Exec(ctx, `delete from catalog_item_versions where id = $1 and state = 'draft'`, versionID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrVersionState
	}
	return err
}

// PublishVersion makes a draft or a deprecated version the published one;
// the version published until now is deprecated. A changelog, when given,
// replaces a draft's.
func (s *Store) PublishVersion(ctx context.Context, itemID, versionID string, changelog *string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select 1 from catalog_items where id = $1 for update`, itemID); err != nil {
			return err
		}
		var state string
		err := tx.QueryRow(ctx, `select state from catalog_item_versions where id = $1 and item_id = $2`, versionID, itemID).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state == "published" {
			return ErrVersionState
		}
		if _, err := tx.Exec(ctx, `update catalog_item_versions set state = 'deprecated', deprecated_at = now()
			where item_id = $1 and state = 'published'`, itemID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update catalog_item_versions set state = 'published', published_at = now(), deprecated_at = null,
			changelog = case when state = 'draft' then coalesce($2, changelog) else changelog end where id = $1`,
			versionID, changelog); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update catalog_items set published_version_id = $2, updated_at = now() where id = $1`, itemID, versionID)
		return err
	})
}

// DeprecateVersion withdraws the published version: the item has none left.
func (s *Store) DeprecateVersion(ctx context.Context, itemID, versionID string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update catalog_item_versions set state = 'deprecated', deprecated_at = now()
			where id = $1 and item_id = $2 and state = 'published'`, versionID, itemID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrVersionState
		}
		_, err = tx.Exec(ctx, `update catalog_items set published_version_id = null, updated_at = now() where id = $1`, itemID)
		return err
	})
}

// SetEntitlements opens an item to every project, or to these projects
// only.
func (s *Store) SetEntitlements(ctx context.Context, itemID string, all bool, projectIDs []string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update catalog_items set all_projects = $2, updated_at = now() where id = $1`, itemID, all); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from catalog_entitlements where item_id = $1 and not (project_id = any($2))`,
			itemID, nonNilStrings(projectIDs)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into catalog_entitlements (item_id, project_id) select $1, unnest($2::text[])
			on conflict do nothing`, itemID, nonNilStrings(projectIDs))
		return err
	})
}

// UnknownProjects returns the IDs that are not projects.
func (s *Store) UnknownProjects(ctx context.Context, ids []string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select x.pid from unnest($1::text[]) as x(pid)
		where not exists (select 1 from projects p where p.id = x.pid)`, nonNilStrings(ids))
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// AdminCategory is a category with its number of items, in any state.
type AdminCategory struct {
	Key       string `db:"key"`
	Name      string `db:"name"`
	SortOrder int    `db:"sort_order"`
	Items     int    `db:"items"`
}

func (s *Store) AdminCategories(ctx context.Context) ([]AdminCategory, error) {
	rows, _ := s.Pool.Query(ctx, `select c.key, c.name, c.sort_order,
		(select count(*) from catalog_items i where i.category = c.key)::int as items
		from catalog_categories c order by c.sort_order, c.key`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AdminCategory])
}

// CreateCategory adds a category (ErrInUse when the key is taken).
func (s *Store) CreateCategory(ctx context.Context, c AdminCategory) error {
	_, err := s.Pool.Exec(ctx, `insert into catalog_categories (key, name, sort_order) values ($1, $2, $3)`, c.Key, c.Name, c.SortOrder)
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

func (s *Store) UpdateCategory(ctx context.Context, key string, name *string, sortOrder *int) error {
	tag, err := s.Pool.Exec(ctx, `update catalog_categories set name = coalesce($2, name), sort_order = coalesce($3, sort_order)
		where key = $1`, key, name, sortOrder)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteCategory removes an empty category (ErrInUse when items are in it).
func (s *Store) DeleteCategory(ctx context.Context, key string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from catalog_items where category = $1)`, key).Scan(&used); err != nil {
			return err
		}
		if used {
			return ErrInUse
		}
		tag, err := tx.Exec(ctx, `delete from catalog_categories where key = $1`, key)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// ImportVersion is a version an import adds, in its state.
type ImportVersion struct {
	NewVersion
	State string // draft or deprecated
}

// ImportItem creates an item with imported versions (ErrInUse when the slug
// is taken).
func (s *Store) ImportItem(ctx context.Context, it NewItem, versions []ImportVersion) error {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into catalog_items (id, slug, name, summary, description_md, category, type, icon, tags,
			featured) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			it.ID, it.Slug, it.Name, it.Summary, it.DescriptionMD, it.Category, it.Type, it.Icon, nonNilStrings(it.Tags), it.Featured); err != nil {
			return err
		}
		for _, v := range versions {
			v.ItemID = it.ID
			if _, err := tx.Exec(ctx, `insert into catalog_item_versions (id, item_id, number, state, form_schema, ui_schema,
				initial_values, blueprint, changelog, created_by, deprecated_at)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, case when $4 = 'deprecated' then now() end)`,
				v.ID, v.ItemID, v.Number, v.State, v.Def.FormSchema, v.Def.UISchema, jsonOr(v.Def.InitialValues, "{}"),
				jsonOr(v.Def.Blueprint, "{}"), v.Changelog, v.CreatedBy); err != nil {
				return err
			}
		}
		return nil
	})
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}
