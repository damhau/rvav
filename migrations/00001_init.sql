-- +goose Up
-- Core schema for slice 1. IDs are opaque text (prefixed ULIDs) created by
-- the application; see internal/ids.

create table environments (
    key         text primary key check (key ~ '^[a-z][a-z0-9]{1,9}$'),
    name        text not null,
    production  boolean not null default false,
    sort_order  int not null default 0
);

create table users (
    id            text primary key,
    subject       text not null unique,
    name          text not null,
    email         text not null default '',
    title         text not null default '',
    global_roles  text[] not null default '{user}',
    created_at    timestamptz not null default now(),
    last_seen_at  timestamptz
);
create index users_email_idx on users (lower(email));

create table api_tokens (
    id            text primary key,
    user_id       text not null references users on delete cascade,
    name          text not null,
    token_hash    text not null unique,
    created_at    timestamptz not null default now(),
    expires_at    timestamptz,
    last_used_at  timestamptz
);

create table projects (
    id            text primary key,
    code          text not null unique check (code ~ '^[a-z][a-z0-9]{1,7}$'),
    name          text not null,
    description   text not null default '',
    cost_center   text not null default '',
    environments  text[] not null default '{}',
    created_at    timestamptz not null default now()
);

create table project_members (
    project_id  text not null references projects on delete cascade,
    user_id     text not null references users on delete cascade,
    role        text not null check (role in ('project-admin', 'member', 'viewer')),
    primary key (project_id, user_id)
);

-- OIDC group -> role. project_id null means a global role.
create table group_mappings (
    oidc_group  text not null,
    project_id  text references projects on delete cascade,
    role        text not null,
    unique nulls not distinct (oidc_group, project_id, role)
);

-- environment '' means the project-wide quota.
create table quotas (
    project_id   text not null references projects on delete cascade,
    environment  text not null default '',
    resource     text not null check (resource in ('vcpu', 'memory_gb', 'storage_gb', 'vm_count')),
    limit_value  int,
    enforcement  text not null default 'hard' check (enforcement in ('hard', 'soft')),
    primary key (project_id, environment, resource)
);

-- Where a project may place VMs in an environment. Patterns are globs
-- matched against names in the vCenter inventory cache.
create table placement_rules (
    id                  text primary key,
    project_id          text not null references projects on delete cascade,
    environment         text not null references environments,
    priority            int not null default 100,
    cluster_patterns    text[] not null,
    datastore_patterns  text[] not null,
    network_patterns    text[] not null,
    folder              text not null
);

-- vSphere port group -> EfficientIP subnet.
create table network_subnets (
    network                text primary key,
    subnet                 text not null,
    gateway                text not null,
    dns_zone               text not null,
    dns_servers            text[] not null default '{}',
    efficientip_subnet_id  text not null default ''
);

create table lease_policies (
    environment     text primary key references environments,
    default_days    int,
    max_days        int,
    max_extensions  int not null default 0,
    expiry_action   text not null default 'power_off_then_delete'
);

create table catalog_categories (
    key         text primary key,
    name        text not null,
    sort_order  int not null default 0
);

create table catalog_items (
    id                    text primary key,
    slug                  text not null unique,
    name                  text not null,
    summary               text not null,
    description_md        text not null default '',
    category              text not null references catalog_categories,
    type                  text not null check (type in ('vm_blueprint', 'awx_job_template', 'awx_workflow', 'composite')),
    icon                  text not null default 'server',
    tags                  text[] not null default '{}',
    featured              boolean not null default false,
    source_kind           text not null default 'native' check (source_kind in ('native', 'awx')),
    awx_template_id       int,
    awx_template_name     text,
    published_version_id  text,
    created_at            timestamptz not null default now()
);

create table catalog_item_versions (
    id              text primary key,
    item_id         text not null references catalog_items on delete cascade,
    number          text not null,
    state           text not null check (state in ('draft', 'published', 'deprecated')),
    form_schema     jsonb not null,
    ui_schema       jsonb not null,
    initial_values  jsonb not null default '{}',
    blueprint       jsonb not null default '{}',
    changelog       text not null default '',
    published_at    timestamptz,
    created_at      timestamptz not null default now(),
    unique (item_id, number)
);

alter table catalog_items
    add constraint catalog_items_published_version_fk
    foreign key (published_version_id) references catalog_item_versions deferrable initially deferred;

create table catalog_entitlements (
    item_id     text not null references catalog_items on delete cascade,
    project_id  text not null references projects on delete cascade,
    primary key (item_id, project_id)
);

create table favourites (
    user_id     text not null references users on delete cascade,
    item_id     text not null references catalog_items on delete cascade,
    created_at  timestamptz not null default now(),
    primary key (user_id, item_id)
);

-- Conditions are CEL over inputs, environment, project, item and vm.
create table approval_policies (
    id          text primary key,
    name        text not null,
    reason      text not null,
    condition   text not null,
    item_type   text,
    project_id  text references projects on delete cascade,
    levels      jsonb not null,
    priority    int not null default 100,
    enabled     boolean not null default true
);

create sequence request_number_seq start 10001;

create table requests (
    id                 text primary key,
    number             text not null unique,
    item_id            text not null references catalog_items,
    version_id         text not null references catalog_item_versions,
    project_id         text not null references projects,
    environment        text not null references environments,
    title              text not null,
    status             text not null check (status in ('pending_approval', 'scheduled', 'queued', 'running', 'succeeded', 'failed', 'rejected', 'cancelling', 'cancelled')),
    requester_id       text not null references users,
    on_behalf_of_id    text references users,
    owner_id           text not null references users,
    inputs             jsonb not null,
    count              int not null default 1,
    justification      text not null default '',
    lease_days         int,
    approval_required  boolean not null default false,
    approval           jsonb not null default '{}',
    plan               jsonb not null default '{}',
    submitted_at       timestamptz not null default now(),
    scheduled_at       timestamptz,
    started_at         timestamptz,
    finished_at        timestamptz,
    workflow_id        text,
    error_step_id      text,
    error_message      text
);
create index requests_project_idx on requests (project_id, submitted_at desc, id desc);
create index requests_requester_idx on requests (requester_id, submitted_at desc);
create index requests_status_idx on requests (status);

create table request_steps (
    id             text primary key,
    request_id     text not null references requests on delete cascade,
    parent_id      text references request_steps on delete cascade,
    position       int not null,
    key            text not null,
    title          text not null,
    detail         text not null default '',
    system         text check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx')),
    status         text not null default 'pending' check (status in ('pending', 'running', 'done', 'failed', 'skipped', 'cancelled')),
    started_at     timestamptz,
    finished_at    timestamptz,
    error          text,
    external_kind  text,
    external_id    text,
    external_url   text,
    has_log        boolean not null default false,
    unique (request_id, key)
);
create index request_steps_request_idx on request_steps (request_id, parent_id, position);

create table step_log_lines (
    step_id  text not null references request_steps on delete cascade,
    n        int not null,
    text     text not null,
    kind     text not null default 'plain',
    primary key (step_id, n)
);

create table deployments (
    id                     text primary key,
    name                   text not null,
    status                 text not null check (status in ('provisioning', 'ready', 'updating', 'failed', 'deleting', 'deleted')),
    project_id             text not null references projects,
    environment            text not null references environments,
    item_id                text not null references catalog_items,
    version_id             text not null references catalog_item_versions,
    owner_id               text not null references users,
    created_by_request_id  text references requests,
    created_at             timestamptz not null default now(),
    lease_expires_at       timestamptz,
    lease_extensions_used  int not null default 0,
    lease_extensions_max   int,
    expiry_action          text not null default 'power_off_then_delete',
    sync_state             text not null default 'in_sync' check (sync_state in ('in_sync', 'drifted', 'missing')),
    inputs                 jsonb not null default '{}',
    deleted_at             timestamptz
);
create index deployments_project_idx on deployments (project_id, name, id);

create table resources (
    id             text primary key,
    deployment_id  text not null references deployments on delete cascade,
    request_id     text references requests,
    type           text not null check (type in ('vsphere.vm', 'ipam.address', 'dns.record')),
    name           text not null,
    external_id    text,
    sync_state     text not null default 'in_sync' check (sync_state in ('in_sync', 'drifted', 'missing')),
    properties     jsonb not null default '{}',
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),
    unique (type, external_id)
);
create index resources_deployment_idx on resources (deployment_id);

-- vCenter inventory cache, written by `rvav sync`.
create table inv_clusters (
    vcenter          text not null,
    moref            text not null,
    name             text not null,
    datacenter       text not null default '',
    host_count       int not null default 0,
    cpu_total_mhz    bigint not null default 0,
    cpu_used_mhz     bigint not null default 0,
    memory_total_mb  bigint not null default 0,
    memory_used_mb   bigint not null default 0,
    synced_at        timestamptz not null default now(),
    primary key (vcenter, moref)
);

create table inv_datastores (
    vcenter      text not null,
    moref        text not null,
    name         text not null,
    capacity_gb  bigint not null default 0,
    free_gb      bigint not null default 0,
    clusters     text[] not null default '{}',
    synced_at    timestamptz not null default now(),
    primary key (vcenter, moref)
);

create table inv_networks (
    vcenter    text not null,
    moref      text not null,
    name       text not null,
    kind       text not null default '',
    clusters   text[] not null default '{}',
    synced_at  timestamptz not null default now(),
    primary key (vcenter, moref)
);

create table inv_templates (
    vcenter    text not null,
    moref      text not null,
    name       text not null,
    guest_os   text not null default '',
    synced_at  timestamptz not null default now(),
    primary key (vcenter, moref)
);

create table inv_vms (
    vcenter        text not null,
    moref          text not null,
    instance_uuid  text not null default '',
    name           text not null,
    power_state    text not null default '',
    guest_state    text not null default '',
    ip_address     text not null default '',
    hostname       text not null default '',
    guest_os       text not null default '',
    cpu_count      int not null default 0,
    memory_mb      int not null default 0,
    cluster        text not null default '',
    host           text not null default '',
    cpu_pct        real,
    memory_pct     real,
    snapshot_count int not null default 0,
    synced_at      timestamptz not null default now(),
    primary key (vcenter, moref)
);
create index inv_vms_name_idx on inv_vms (name);
create index inv_vms_uuid_idx on inv_vms (instance_uuid);

create table name_counters (
    prefix      text primary key,
    last_value  int not null
);

-- Event outbox for /events. Inserting a row also sends pg_notify.
create table events (
    id          bigserial primary key,
    type        text not null,
    project_id  text,
    topics      text[] not null,
    payload     jsonb not null,
    created_at  timestamptz not null default now()
);
create index events_created_idx on events (created_at);

create table idempotency_keys (
    user_id      text not null references users on delete cascade,
    key          uuid not null,
    body_hash    text not null,
    status_code  int,
    response     jsonb,
    created_at   timestamptz not null default now(),
    primary key (user_id, key)
);

-- +goose Down
drop table if exists idempotency_keys, events, name_counters, inv_vms, inv_templates, inv_networks,
    inv_datastores, inv_clusters, resources, deployments, step_log_lines, request_steps, requests,
    approval_policies, favourites, catalog_entitlements, catalog_item_versions, catalog_items,
    catalog_categories, lease_policies, network_subnets, placement_rules, quotas, group_mappings,
    project_members, projects, api_tokens, users, environments cascade;
drop sequence if exists request_number_seq;
