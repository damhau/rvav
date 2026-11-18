-- +goose Up
-- The key pair rvav makes for a deployment whose blueprint asks for a
-- generated key (ADR-0039): the public key goes on its VMs, the private
-- key is sealed until its owner downloads it once, then forgotten.
create table deployment_keys (
    deployment_id  text primary key references deployments on delete cascade,
    username       text not null,
    public_key     text not null,
    fingerprint    text not null,
    key_id         text,
    nonce          bytea,
    ciphertext     bytea,
    created_at     timestamptz not null default now(),
    downloaded_at  timestamptz,
    downloaded_by  text references users on delete set null,
    check ((ciphertext is null) = (nonce is null))
);

-- The user a component's VMs were given, for the deployment page.
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet',
                    'nsx.group', 'nsx.policy', 'nsx.membership', 'guest.access'));

-- +goose Down
delete from resources where type = 'guest.access';
alter table resources drop constraint resources_type_check;
alter table resources add constraint resources_type_check
    check (type in ('vsphere.vm', 'ipam.address', 'dns.record', 'vsphere.drs_rule', 'nsx.segment', 'ipam.subnet',
                    'nsx.group', 'nsx.policy', 'nsx.membership'));
drop table if exists deployment_keys;
