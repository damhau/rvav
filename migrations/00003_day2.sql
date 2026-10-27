-- +goose Up
-- Day-2 actions (docs/adr/0013-day2-actions.md) are requests on a
-- deployment. The resource is kept by ID and name: resources are deleted
-- with what they describe, the history is not.
alter table requests add column kind text not null default 'provision' check (kind in ('provision', 'day2'));
alter table requests add column action text;
alter table requests add column deployment_id text references deployments;
alter table requests add column resource_id text;
alter table requests add column resource_name text;
create index requests_deployment_idx on requests (deployment_id, submitted_at desc, id desc) where deployment_id is not null;

-- +goose Down
drop index if exists requests_deployment_idx;
alter table requests drop column if exists resource_name, drop column if exists resource_id,
    drop column if exists deployment_id, drop column if exists action, drop column if exists kind;
