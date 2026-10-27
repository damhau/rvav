-- +goose Up
-- Secrets value sources (and later the integration settings) use
-- (docs/adr/0019-catalog-administration.md): AES-256-GCM with the key in
-- RVAV_SECRETS_KEY, the name as additional data. Values are never returned.
create table secrets (
    name         text primary key,
    kind         text not null check (kind in ('token', 'basic', 'header')),
    description  text not null default '',
    username     text not null default '',
    header       text not null default '',
    nonce        bytea not null,
    ciphertext   bytea not null,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now()
);

-- +goose Down
drop table if exists secrets;
