-- +goose Up
-- C2.3 (ADR-0047): deployment limit policies, global or a project's, for
-- every environment or one, each limit decided on its own by priority.
create table deployment_limit_policies (
    id                       text primary key,
    name                     text not null,
    project_id               text references projects on delete cascade,
    environment              text references environments on delete cascade,
    priority                 int not null default 100,
    enabled                  boolean not null default true,
    enforcement              text not null default 'hard' check (enforcement in ('hard', 'soft')),
    deployments_per_user     int check (deployments_per_user >= 0),
    deployments_per_project  int check (deployments_per_project >= 0),
    vcpu_per_deployment      int check (vcpu_per_deployment >= 1),
    memory_gb_per_deployment int check (memory_gb_per_deployment >= 1),
    set_by_admin             boolean not null default true,
    created_at               timestamptz not null default now()
);

-- +goose Down
drop table deployment_limit_policies;
