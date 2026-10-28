-- +goose Up
-- Naming policies (docs/adr/0020-administration.md), offered to projects
-- and environments like templates and sizes (offers, kind 'naming'). A
-- counter is kept per name prefix (as before) or one per policy
-- (name_counters key policy:<id>).
create table naming_policies (
    id         text primary key,
    name       text not null,
    template   text not null,
    counter    text not null default 'prefix' check (counter in ('prefix', 'policy')),
    created_at timestamptz not null default now()
);

-- +goose Down
delete from offers where kind = 'naming';
drop table naming_policies;
