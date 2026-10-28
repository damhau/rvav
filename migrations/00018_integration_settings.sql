-- +goose Up
-- Integration settings and key rotation (docs/adr/0020-administration.md):
-- the vCenter, AWX and EfficientIP connections set from the UI, their
-- password or token sealed like other secrets; environment variables win.
-- Each sealed value records which key sealed it.
alter table secrets add column key_id text not null default '';

create sequence settings_revision;

create table integration_settings (
    kind              text primary key check (kind in ('vcenter', 'awx', 'efficientip')),
    settings          jsonb not null default '{}',
    secret_key_id     text not null default '',
    secret_nonce      bytea,
    secret_ciphertext bytea,
    revision          bigint not null default nextval('settings_revision'),
    updated_at        timestamptz not null default now(),
    updated_by        text references users
);

-- +goose Down
drop table integration_settings;
drop sequence settings_revision;
alter table secrets drop column key_id;
