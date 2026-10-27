-- +goose Up
-- From the review of S8 (docs/adr/0019-catalog-administration.md): a
-- version's revision, so a save or a publish based on an older draft is
-- refused; and items made from AWX before 00011 open to every project, as
-- the ADR says.
alter table catalog_item_versions add column revision integer not null default 1;
update catalog_items set all_projects = true where source_kind = 'awx';

-- +goose Down
alter table catalog_item_versions drop column revision;
