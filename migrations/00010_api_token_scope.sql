-- +goose Up
-- An API token may be limited to one use: "inventory" only reads the
-- Ansible inventory (ADR-0017), whatever its user may do otherwise.
alter table api_tokens add column scope text not null default '' check (scope in ('', 'inventory'));

-- +goose Down
alter table api_tokens drop column if exists scope;
