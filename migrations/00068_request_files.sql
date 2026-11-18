-- +goose Up
-- Files requesters attach to file fields (ADR-0040): kept with their
-- request, in Postgres. One waits a day for its request (request_id null),
-- then goes; a request removed before it started gives its files back.
create table request_files (
    id           text primary key,
    owner_id     text not null references users on delete cascade,
    request_id   text references requests on delete set null,
    name         text not null,
    content_type text not null,
    size         bigint not null check (size > 0),
    sha256       text not null,
    content      bytea not null,
    created_at   timestamptz not null default now()
);
create index request_files_request_idx on request_files (request_id) where request_id is not null;
create index request_files_waiting_idx on request_files (owner_id, created_at) where request_id is null;

-- +goose Down
drop table request_files;
