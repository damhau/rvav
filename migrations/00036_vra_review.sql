-- +goose Up
-- The vRA importer's review (P2.7): a vRA project an administrator keeps in
-- vRA though rvav has a project of its name (no rvav project); the shares
-- the import made, so one an administrator removed isn't made again; the
-- reads kept, newest first.
alter table vra_project_map alter column rvav_project_id drop not null;
alter table vra_links drop constraint vra_links_kind_check;
alter table vra_links add constraint vra_links_kind_check check (kind in ('template', 'deployment', 'share'));
create index vra_snapshots_created on vra_snapshots (created_at desc);

-- +goose Down
drop index vra_snapshots_created;
delete from vra_links where kind = 'share';
alter table vra_links drop constraint vra_links_kind_check;
alter table vra_links add constraint vra_links_kind_check check (kind in ('template', 'deployment'));
delete from vra_project_map where rvav_project_id is null;
alter table vra_project_map alter column rvav_project_id set not null;
