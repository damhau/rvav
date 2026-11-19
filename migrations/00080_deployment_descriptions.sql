-- +goose Up
-- C2.1 (ADR-0046): what a deployment is for, and the names its project's
-- live deployments hold, unique there whatever the case as rvav checks.
alter table deployments add column description text not null default '';
create index deployments_live_name_idx on deployments (project_id, lower(name)) where status <> 'deleted';

-- +goose Down
drop index deployments_live_name_idx;
alter table deployments drop column description;
