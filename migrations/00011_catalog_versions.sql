-- +goose Up
-- Catalog administration (docs/adr/0019-catalog-administration.md): who
-- made a version, when it was deprecated, and at most one draft and one
-- published version per item.
alter table catalog_item_versions
    add column created_by text references users,
    add column deprecated_at timestamptz;
alter table catalog_items
    add column updated_at timestamptz not null default now(),
    -- open to every project, those made later included (catalog_entitlements otherwise)
    add column all_projects boolean not null default false;
create unique index catalog_item_versions_one_draft on catalog_item_versions (item_id) where state = 'draft';
create unique index catalog_item_versions_one_published on catalog_item_versions (item_id) where state = 'published';

-- +goose Down
drop index if exists catalog_item_versions_one_published;
drop index if exists catalog_item_versions_one_draft;
alter table catalog_items drop column if exists updated_at, drop column if exists all_projects;
alter table catalog_item_versions drop column if exists deprecated_at, drop column if exists created_by;
