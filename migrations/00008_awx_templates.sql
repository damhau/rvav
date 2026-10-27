-- +goose Up
-- AWX templates rvav imports into the catalog (docs/adr/0016-awx-catalog.md):
-- the survey AWX has now, and the one the item's form was made from, so a
-- changed survey can be flagged.
create table awx_templates (
    awx_id           int not null,
    workflow         boolean not null,
    name             text not null,
    item_id          text references catalog_items on delete set null,
    survey           jsonb not null default '[]',
    imported_survey  jsonb not null default '[]',
    warnings         text[] not null default '{}',
    gone             boolean not null default false,
    modified_at      timestamptz,
    synced_at        timestamptz not null default now(),
    primary key (awx_id, workflow)
);

insert into catalog_categories (key, name, sort_order) values ('automation', 'Automation', 90)
on conflict (key) do nothing;

-- +goose Down
drop table if exists awx_templates;
