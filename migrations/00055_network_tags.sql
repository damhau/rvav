-- +goose Up
-- Constraint tags (docs/adr/0035-constraint-tags.md): the vSphere tags the
-- inventory sync reads on networks, as category/tag, as it does on clusters
-- and datastores.
alter table inv_networks add column tags text[] not null default '{}';

-- +goose Down
alter table inv_networks drop column tags;
