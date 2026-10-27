package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// AWXTemplate is an AWX template rvav imports, and the item made from it.
type AWXTemplate struct {
	AWXID          int             `db:"awx_id"`
	Workflow       bool            `db:"workflow"`
	Name           string          `db:"name"`
	ItemID         *string         `db:"item_id"`
	ItemName       *string         `db:"item_name"`
	Survey         json.RawMessage `db:"survey"`
	ImportedSurvey json.RawMessage `db:"imported_survey"`
	Warnings       []string        `db:"warnings"`
	Gone           bool            `db:"gone"`
	ModifiedAt     *time.Time      `db:"modified_at"`
	SyncedAt       time.Time       `db:"synced_at"`
}

const awxTemplateCols = `t.awx_id, t.workflow, t.name, t.item_id, i.name as item_name, t.survey, t.imported_survey,
	t.warnings, t.gone, t.modified_at, t.synced_at`

func (s *Store) AWXTemplates(ctx context.Context) ([]AWXTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select `+awxTemplateCols+` from awx_templates t
		left join catalog_items i on i.id = t.item_id order by t.name, t.workflow`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AWXTemplate])
}

// AWXTemplateForItem is the AWX template an item was made from.
func (s *Store) AWXTemplateForItem(ctx context.Context, itemID string) (AWXTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select `+awxTemplateCols+` from awx_templates t
		left join catalog_items i on i.id = t.item_id where t.item_id = $1`, itemID)
	t, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[AWXTemplate])
	return t, notFound(err)
}

// FindAWXItem returns the AWX-sourced item launching this template, made by
// hand or by the seed (ErrNotFound when none).
func (s *Store) FindAWXItem(ctx context.Context, name string, workflow bool) (string, error) {
	typ := "awx_job_template"
	if workflow {
		typ = "awx_workflow"
	}
	var id string
	err := s.Pool.QueryRow(ctx, `select id from catalog_items where source_kind = 'awx' and awx_template_name = $1 and type = $2
		order by created_at limit 1`, name, typ).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// NewAWXItem is an item made from an AWX template, published at once and
// open to every project.
type NewAWXItem struct {
	ID, VersionID, Slug, Name, Summary, Category, Type, Icon string
	AWXID                                                    int
	AWXName                                                  string
	Schema, UI, Initial, Blueprint                           json.RawMessage
}

func (s *Store) CreateAWXItem(ctx context.Context, it NewAWXItem) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		slug := it.Slug
		for n := 2; ; n++ {
			var taken bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from catalog_items where slug = $1)`, slug).Scan(&taken); err != nil {
				return err
			}
			if !taken {
				break
			}
			slug = it.Slug + "-" + strconv.Itoa(n)
		}
		if _, err := tx.Exec(ctx, `insert into catalog_items (id, slug, name, summary, category, type, icon, source_kind,
			awx_template_id, awx_template_name) values ($1, $2, $3, $4, $5, $6, $7, 'awx', $8, $9)`,
			it.ID, slug, it.Name, it.Summary, it.Category, it.Type, it.Icon, it.AWXID, it.AWXName); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into catalog_item_versions (id, item_id, number, state, form_schema, ui_schema,
			initial_values, blueprint, changelog, published_at) values ($1, $2, 'AWX #1', 'published', $3, $4, $5, $6,
			'Made from the AWX survey.', now())`, it.VersionID, it.ID, it.Schema, it.UI, it.Initial, it.Blueprint); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update catalog_items set published_version_id = $2 where id = $1`, it.ID, it.VersionID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into catalog_entitlements (item_id, project_id) select $1, id from projects`, it.ID)
		return err
	})
}

// UpsertAWXTemplate records a template as AWX has it now. The imported
// survey is only set when importedSurvey is not nil (the item was just made
// or linked).
func (s *Store) UpsertAWXTemplate(ctx context.Context, t AWXTemplate, importedSurvey json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `insert into awx_templates (awx_id, workflow, name, item_id, survey, imported_survey, warnings,
		gone, modified_at, synced_at) values ($1, $2, $3, $4, $5, coalesce($6, '[]'::jsonb), $7, false, $8, now())
		on conflict (awx_id, workflow) do update set name = excluded.name, item_id = coalesce(excluded.item_id, awx_templates.item_id),
			survey = excluded.survey, imported_survey = coalesce($6, awx_templates.imported_survey), warnings = excluded.warnings,
			gone = false, modified_at = excluded.modified_at, synced_at = now()`,
		t.AWXID, t.Workflow, t.Name, t.ItemID, t.Survey, importedSurvey, nonNilStrings(t.Warnings), t.ModifiedAt)
	if err == nil && t.ItemID != nil {
		_, err = s.Pool.Exec(ctx, `update catalog_items set awx_template_id = $2 where id = $1`, *t.ItemID, t.AWXID)
	}
	return err
}

// MarkAWXTemplatesGone flags the templates a sync did not see (seen lists
// their IDs and whether each is a workflow).
func (s *Store) MarkAWXTemplatesGone(ctx context.Context, seenIDs []int, seenWorkflow []bool) (int, error) {
	tag, err := s.Pool.Exec(ctx, `update awx_templates t set gone = true where not gone and not exists (
		select 1 from unnest($1::int[], $2::bool[]) as seen (id, workflow) where seen.id = t.awx_id and seen.workflow = t.workflow)`,
		seenIDs, seenWorkflow)
	return int(tag.RowsAffected()), err
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Store) CategoryExists(ctx context.Context, key string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from catalog_categories where key = $1)`, key).Scan(&ok)
	return ok, err
}
