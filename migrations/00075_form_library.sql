-- +goose Up
-- The form library (ADR-0043): value sources and validations, REST or CEL,
-- that forms name at a version. A version never changes; saving one adds
-- the next.
create table form_library (
    key          text not null,
    version      integer not null check (version >= 1),
    name         text not null,
    description  text not null default '',
    use          text not null check (use in ('source', 'validation')),
    kind         text not null check (kind in ('rest', 'cel')),
    params       jsonb not null default '{}',
    search       boolean not null default false,
    parameters   jsonb not null default '[]',
    message      text not null default '',
    created_by   text references users on delete set null,
    created_at   timestamptz not null default now(),
    primary key (key, version)
);

-- +goose Down
drop table if exists form_library;
