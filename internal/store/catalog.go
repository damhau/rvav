package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// itemSelect returns catalog items with their published version, the
// caller's favourite flag ($1 = user ID) and usage numbers.
const itemSelect = `
select i.id, i.slug, i.name, i.summary, i.description_md, i.category, i.type, i.icon, i.tags,
       i.featured, i.source_kind, i.awx_template_id, i.awx_template_name, i.published_version_id,
       v.number as version_number, v.published_at as version_published_at,
       exists (select 1 from favourites f where f.item_id = i.id and f.user_id = $1) as favourite,
       (select count(*) from requests r where r.item_id = i.id
          and r.submitted_at > now() - interval '30 days')::int as requests_30d,
       (select percentile_disc(0.5) within group (order by extract(epoch from r.finished_at - r.started_at))::int
          from (select started_at, finished_at from requests
                where item_id = i.id and status = 'succeeded' and started_at is not null
                order by finished_at desc limit 30) r) as typical_seconds,
       exists (select 1 from approval_policies p where p.enabled
          and (p.item_type is null or p.item_type = i.type)) as has_approval_policy,
       exists (select 1 from approval_policies p where p.enabled and p.condition = 'true'
          and (p.item_type is null or p.item_type = i.type)) as always_approval
from catalog_items i
left join catalog_item_versions v on v.id = i.published_version_id`

type ItemFilter struct {
	ProjectID string
	UserID    string
	Category  string
	Type      string
	Favourite bool
	Search    string
	Limit     int
	After     *Cursor
}

// ListItems returns published items entitled to a project, ordered by name.
func (s *Store) ListItems(ctx context.Context, f ItemFilter) (Page[CatalogItem], error) {
	after := Cursor{}
	if f.After != nil {
		after = *f.After
	}
	rows, _ := s.Pool.Query(ctx, itemSelect+`
		where i.published_version_id is not null
		  and exists (select 1 from catalog_entitlements e where e.item_id = i.id and e.project_id = $2)
		  and ($3 = '' or i.category = $3)
		  and ($4 = '' or i.type = $4)
		  and (not $5 or exists (select 1 from favourites f where f.item_id = i.id and f.user_id = $1))
		  and ($6 = '' or i.name ilike $6 or i.summary ilike $6 or array_to_string(i.tags, ' ') ilike $6)
		  and ($7 = '' or (i.name, i.id) > ($7, $8))
		order by i.name, i.id limit $9`,
		f.UserID, f.ProjectID, f.Category, f.Type, f.Favourite, SearchText(f.Search), after.Value, after.ID, f.Limit+1)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[CatalogItem])
	if err != nil {
		return Page[CatalogItem]{}, err
	}
	return paginate(list, f.Limit, func(i CatalogItem) Cursor { return Cursor{Value: i.Name, ID: i.ID} }), nil
}

// GetItem finds an item by ID or slug.
func (s *Store) GetItem(ctx context.Context, idOrSlug, userID string) (CatalogItem, error) {
	rows, _ := s.Pool.Query(ctx, itemSelect+` where i.id = $2 or i.slug = $2`, userID, idOrSlug)
	i, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[CatalogItem])
	return i, notFound(err)
}

func (s *Store) IsEntitled(ctx context.Context, itemID, projectID string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from catalog_entitlements
		where item_id = $1 and project_id = $2)`, itemID, projectID).Scan(&ok)
	return ok, err
}

func (s *Store) ListCategories(ctx context.Context, projectID string) ([]Category, error) {
	rows, _ := s.Pool.Query(ctx, `select c.key, c.name,
		(select count(*) from catalog_items i
		 join catalog_entitlements e on e.item_id = i.id and e.project_id = $1
		 where i.category = c.key and i.published_version_id is not null)::int as item_count
		from catalog_categories c order by c.sort_order, c.key`, projectID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Category])
}

const versionCols = `id, item_id, number, state, form_schema, ui_schema, initial_values, blueprint, published_at`

func (s *Store) GetVersion(ctx context.Context, id string) (ItemVersion, error) {
	rows, _ := s.Pool.Query(ctx, `select `+versionCols+` from catalog_item_versions where id = $1`, id)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ItemVersion])
	return v, notFound(err)
}

func (s *Store) SetFavourite(ctx context.Context, userID, itemID string, on bool) error {
	var err error
	if on {
		_, err = s.Pool.Exec(ctx, `insert into favourites (user_id, item_id) values ($1, $2) on conflict do nothing`, userID, itemID)
	} else {
		_, err = s.Pool.Exec(ctx, `delete from favourites where user_id = $1 and item_id = $2`, userID, itemID)
	}
	return err
}

// ApprovalPolicies returns enabled policies that apply to an item type in a project.
func (s *Store) ApprovalPolicies(ctx context.Context, projectID, itemType string) ([]ApprovalPolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select id, name, reason, condition, item_type, project_id, levels, priority, auto_approve
		from approval_policies
		where enabled and (item_type is null or item_type = $2) and (project_id is null or project_id = $1)
		order by priority, id`, projectID, itemType)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ApprovalPolicy])
}

// Day2Policies returns the enabled day-2 policies that apply to the project, by priority.
func (s *Store) Day2Policies(ctx context.Context, projectID string) ([]Day2Policy, error) {
	rows, _ := s.Pool.Query(ctx, `select id, name, reason, priority, actions, roles, condition, effect, levels, project_id
		from day2_policies where enabled and (project_id is null or project_id = $1) order by priority, id`, projectID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Day2Policy])
}
