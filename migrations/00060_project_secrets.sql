-- +goose Up
-- A secret may belong to a project: its requests open it rather than the
-- global secret of the same name, and other projects never do (ADR-0038).
-- A project's is sealed with project:<id>:<name> as associated data, so a
-- row can't be moved from one scope to another.
alter table secrets add column project_id text references projects(id) on delete cascade;
alter table secrets drop constraint secrets_pkey;
alter table secrets add constraint secrets_name_project unique nulls not distinct (name, project_id);

-- +goose Down
delete from secrets where project_id is not null;
alter table secrets drop constraint secrets_name_project;
alter table secrets add primary key (name);
alter table secrets drop column project_id;
