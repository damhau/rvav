-- +goose Up
-- The setup checklist (docs/adr/0030-settings-and-local-administrator.md):
-- whether an administrator hid it, so that it no longer opens at sign-in.
-- One row at most.
create table setup_checklist (
    id          boolean primary key default true check (id),
    hidden      boolean not null default false,
    updated_at  timestamptz not null default now(),
    updated_by  text references users on delete set null
);

-- +goose Down
drop table setup_checklist;
