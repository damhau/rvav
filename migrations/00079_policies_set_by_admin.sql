-- +goose Up
-- Whether an administrator wrote a policy last (ADR-0045): a project's
-- admins read such a project policy but don't change or remove it. Rows
-- written before, by an administrator or the seed, are theirs.
alter table approval_policies add column set_by_admin boolean not null default true;
alter table day2_policies add column set_by_admin boolean not null default true;
alter table lease_policies add column set_by_admin boolean not null default true;

-- +goose Down
alter table lease_policies drop column set_by_admin;
alter table day2_policies drop column set_by_admin;
alter table approval_policies drop column set_by_admin;
