-- +goose Up
-- Administration (docs/adr/0020-administration.md): projects are archived,
-- not deleted; group mappings get an ID to be removed by.
alter table projects add column archived_at timestamptz;

alter table group_mappings add column id text;
update group_mappings set id = 'gmp_' || substr(md5(oidc_group || coalesce(project_id, '') || role), 1, 20);
alter table group_mappings alter column id set not null;
alter table group_mappings add primary key (id);

-- +goose Down
alter table group_mappings drop constraint group_mappings_pkey;
alter table group_mappings drop column id;
alter table projects drop column archived_at;
