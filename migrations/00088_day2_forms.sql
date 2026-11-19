-- +goose Up
-- Forms designed for custom day-2 actions (docs/adr/0050-designed-day2-forms.md):
-- one per AWX (awx.*) or workflow (wf.*) action; while active, the action
-- asks for it instead of its survey's or input schema's form.
create table day2_forms (
    action         text primary key,
    form_schema    jsonb not null,
    ui_schema      jsonb not null,
    initial_values jsonb not null default '{}',
    active         boolean not null default false,
    revision       int not null default 1,
    updated_by     text references users on delete set null,
    updated_at     timestamptz not null default now()
);

-- +goose Down
drop table if exists day2_forms;
