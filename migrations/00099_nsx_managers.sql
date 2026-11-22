-- +goose Up
-- Several NSX Managers (docs/adr/0062-several-nsx-managers.md): those added
-- besides the NSX integration's, their password sealed like a vCenter's;
-- each vCenter served by one at most. In vcenter_nsx, '' is the vCenter
-- integration's vCenter, or the NSX integration's Manager.
create table nsx_managers (
    name         text primary key,
    url          text not null,
    username     text not null default '',
    cert_file    text not null default '',
    key_file     text not null default '',
    insecure     boolean not null default false,
    cert_sha256  text not null default '',
    key_id       text not null default '',
    nonce        bytea,
    ciphertext   bytea,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now()
);
create table vcenter_nsx (
    vcenter  text primary key,
    manager  text not null
);
-- Every vCenter's VMs went to the one Manager: they still do.
insert into vcenter_nsx (vcenter, manager) values ('', '');
insert into vcenter_nsx (vcenter, manager) select name, '' from vcenters;

-- +goose Down
drop table vcenter_nsx;
drop table nsx_managers;
