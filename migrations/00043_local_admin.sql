-- +goose Up
-- The local administrator and its sessions (docs/adr/0030-settings-and-local-administrator.md).
-- The API makes the account at first start; it is the only one whose
-- password rvav keeps.
create table local_accounts (
    user_id              text primary key references users on delete cascade,
    username             text not null unique,
    -- argon2id, in the PHC string format.
    password_hash        text not null,
    -- Set at first start and by a reset: the password must be changed
    -- before anything else.
    must_change          boolean not null default true,
    -- Failures in a row; from the third, attempts wait until locked_until.
    failed_attempts      integer not null default 0,
    locked_until         timestamptz,
    password_changed_at  timestamptz not null default now(),
    last_sign_in_at      timestamptz,
    created_at           timestamptz not null default now()
);

-- A local sign-in, by the SHA-256 hash of the cookie's value.
create table sessions (
    id_hash       text primary key,
    user_id       text not null references users on delete cascade,
    created_at    timestamptz not null default now(),
    last_seen_at  timestamptz not null default now(),
    expires_at    timestamptz not null
);
create index sessions_user_idx on sessions (user_id);

-- +goose Down
drop table sessions;
drop table local_accounts;
