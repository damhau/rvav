-- +goose Up
-- Several vCenters (docs/adr/0024-multi-vm-blueprints.md): those added
-- besides the vCenter integration's, their password sealed like a
-- secret's; placement rules may name the vCenter their clusters are in.
create table vcenters (
    name        text primary key,
    url         text not null,
    username    text not null,
    insecure    boolean not null default false,
    key_id      text not null default '',
    nonce       bytea,
    ciphertext  bytea,
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now()
);
alter table placement_rules add column vcenter text not null default '';

-- +goose Down
alter table placement_rules drop column vcenter;
drop table vcenters;
