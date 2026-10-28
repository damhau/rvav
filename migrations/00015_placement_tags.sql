-- +goose Up
-- Placement by vSphere tag (docs/adr/0020-administration.md): the tags the
-- inventory sync reads on clusters and datastores, as category/tag, and
-- the tags a placement rule asks for.
alter table inv_clusters add column tags text[] not null default '{}';
alter table inv_datastores add column tags text[] not null default '{}';
alter table placement_rules add column cluster_tags text[] not null default '{}';
alter table placement_rules add column datastore_tags text[] not null default '{}';

-- +goose Down
alter table placement_rules drop column datastore_tags, drop column cluster_tags;
alter table inv_datastores drop column tags;
alter table inv_clusters drop column tags;
