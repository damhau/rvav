package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// shareTables are where the shares of an item, a category or a source are
// kept (ADR-0019, Sharing): its own row has its every-project share
// (all_projects, share_*), and a table its shares with chosen projects.
type shareTables struct {
	owner, key      string
	projects, owned string
}

var (
	itemShares     = shareTables{"catalog_items", "id", "catalog_entitlements", "item_id"}
	categoryShares = shareTables{"catalog_categories", "key", "catalog_category_entitlements", "category"}
	sourceShares   = shareTables{"catalog_sources", "kind", "catalog_source_entitlements", "source_kind"}
)

// SourceAWX is the source of the items the AWX sync makes or links.
const SourceAWX = "awx"

// shareCondition is the condition that item i is shared in a project, or
// in any project when project is "", with whom covers says: by its own
// shares, its category's or its source's, which add up.
func shareCondition(project string, covers func(users, groups, roles string) string) string {
	inProject := ""
	if project != "" {
		inProject = ` and e.project_id = ` + project
	}
	every := func(o string) string {
		return o + `.all_projects and ` + covers(o+".share_user_ids", o+".share_groups", o+".share_roles")
	}
	chosen := func(table, owned string) string {
		return `exists (select 1 from ` + table + ` e where ` + owned + inProject + ` and ` + covers("e.user_ids", "e.groups", "e.roles") + `)`
	}
	return `((` + every("i") + `)
		or ` + chosen("catalog_entitlements", "e.item_id = i.id") + `
		or exists (select 1 from catalog_categories c where c.key = i.category and ` + every("c") + `)
		or ` + chosen("catalog_category_entitlements", "e.category = i.category") + `
		or exists (select 1 from catalog_sources s where s.kind = i.source_kind and ` + every("s") + `)
		or ` + chosen("catalog_source_entitlements", "e.source_kind = i.source_kind") + `)`
}

// sharedWith is the condition that item i is shared with a requester in a
// project, from the parameters that hold them: a share covers them when it
// names nobody, them, one of their OIDC groups or a role they hold there.
func sharedWith(project, user, groups, roles string) string {
	return shareCondition(project, func(users, gs, rs string) string {
		return `(cardinality(` + users + `) + cardinality(` + gs + `) + cardinality(` + rs + `) = 0 or ` +
			user + ` = any(` + users + `) or ` + gs + ` && ` + groups + `::text[] or ` + rs + ` && ` + roles + `::text[])`
	})
}

// sharedIn is the condition that item i is shared in a project (any when
// ""), whoever its shares name there.
func sharedIn(project string) string {
	return shareCondition(project, func(string, string, string) string { return "true" })
}

func (s *Store) sharing(ctx context.Context, t shareTables, id string) (Sharing, error) {
	var sh Sharing
	err := s.Pool.QueryRow(ctx, `select all_projects, share_user_ids, share_groups, share_roles from `+t.owner+` where `+t.key+` = $1`,
		id).Scan(&sh.AllProjects, &sh.Everywhere.UserIDs, &sh.Everywhere.Groups, &sh.Everywhere.Roles)
	if err != nil {
		return sh, notFound(err)
	}
	rows, _ := s.Pool.Query(ctx, `select project_id, user_ids, groups, roles from `+t.projects+`
		where `+t.owned+` = $1 order by project_id`, id)
	sh.Projects, err = pgx.CollectRows(rows, pgx.RowToStructByName[ProjectShare])
	return sh, err
}

// setSharing shares with exactly these projects and audiences; touch is
// what else the owner's row sets.
func setSharing(ctx context.Context, tx pgx.Tx, t shareTables, id string, sh Sharing, touch string) error {
	tag, err := tx.Exec(ctx, `update `+t.owner+` set all_projects = $2, share_user_ids = $3, share_groups = $4,
		share_roles = $5`+touch+` where `+t.key+` = $1`, id, sh.AllProjects, nonNilStrings(sh.Everywhere.UserIDs),
		nonNilStrings(sh.Everywhere.Groups), nonNilStrings(sh.Everywhere.Roles))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	ids := make([]string, len(sh.Projects))
	for i, p := range sh.Projects {
		ids[i] = p.ProjectID
	}
	if _, err := tx.Exec(ctx, `delete from `+t.projects+` where `+t.owned+` = $1 and not (project_id = any($2))`, id, ids); err != nil {
		return err
	}
	return addShares(ctx, tx, t, id, sh.Projects, true)
}

// addShares shares with projects; with replace, the audience of one
// shared with already becomes this one.
func addShares(ctx context.Context, db DB, t shareTables, id string, shares []ProjectShare, replace bool) error {
	conflict := `do nothing`
	if replace {
		conflict = `(` + t.owned + `, project_id) do update set user_ids = excluded.user_ids, groups = excluded.groups, roles = excluded.roles`
	}
	for _, p := range shares {
		if _, err := db.Exec(ctx, `insert into `+t.projects+` (`+t.owned+`, project_id, user_ids, groups, roles)
			values ($1, $2, $3, $4, $5) on conflict `+conflict, id, p.ProjectID, nonNilStrings(p.UserIDs),
			nonNilStrings(p.Groups), nonNilStrings(p.Roles)); err != nil {
			return err
		}
	}
	return nil
}

// CategorySharing reads who a category's items are shared with through it.
func (s *Store) CategorySharing(ctx context.Context, key string) (Sharing, error) {
	return s.sharing(ctx, categoryShares, key)
}

// SetCategorySharing shares a category's items with exactly these projects
// and audiences.
func (s *Store) SetCategorySharing(ctx context.Context, key string, sh Sharing) error {
	return s.Tx(ctx, func(tx pgx.Tx) error { return setSharing(ctx, tx, categoryShares, key, sh, "") })
}

// SourceSharing reads who a source's items are shared with through it.
func (s *Store) SourceSharing(ctx context.Context, kind string) (Sharing, error) {
	return s.sharing(ctx, sourceShares, kind)
}

// SetSourceSharing shares a source's items with exactly these projects and
// audiences.
func (s *Store) SetSourceSharing(ctx context.Context, kind string, sh Sharing) error {
	return s.Tx(ctx, func(tx pgx.Tx) error { return setSharing(ctx, tx, sourceShares, kind, sh, "") })
}

// InheritedShare is the sharing of an item's category or source, which
// the item has too.
type InheritedShare struct {
	// Kind is "category" or "source"; Key the category's key or the
	// source's kind.
	Kind, Key, Name string
	Sharing
}

// InheritedSharing reads the sharing an item has through its category and
// its source, those that share it somewhere.
func (s *Store) InheritedSharing(ctx context.Context, itemID string) ([]InheritedShare, error) {
	var category, source string
	var categoryName, sourceName *string
	if err := s.Pool.QueryRow(ctx, `select i.category, c.name, i.source_kind, s.name from catalog_items i
		left join catalog_categories c on c.key = i.category left join catalog_sources s on s.kind = i.source_kind
		where i.id = $1`, itemID).Scan(&category, &categoryName, &source, &sourceName); err != nil {
		return nil, notFound(err)
	}
	out := []InheritedShare{}
	for _, o := range []struct {
		kind, key string
		name      *string
		tables    shareTables
	}{{"category", category, categoryName, categoryShares}, {"source", source, sourceName, sourceShares}} {
		if o.name == nil {
			continue
		}
		sh, err := s.sharing(ctx, o.tables, o.key)
		if err != nil {
			return nil, err
		}
		if sh.AllProjects || len(sh.Projects) > 0 {
			out = append(out, InheritedShare{Kind: o.kind, Key: o.key, Name: *o.name, Sharing: sh})
		}
	}
	return out, nil
}

// SourceName is what rvav calls a source.
func (s *Store) SourceName(ctx context.Context, kind string) (string, error) {
	var name string
	err := s.Pool.QueryRow(ctx, `select name from catalog_sources where kind = $1`, kind).Scan(&name)
	return name, notFound(err)
}
