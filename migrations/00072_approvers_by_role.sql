-- +goose Up
-- Approvers by project role (ADR-0041): the roles a level asks, and who
-- held them in the request's project when it opened, users and OIDC groups.
alter table approvals add column roles text[] not null default '{}',
    add column role_ids text[] not null default '{}',
    add column role_groups text[] not null default '{}';

-- +goose Down
alter table approvals drop column roles, drop column role_ids, drop column role_groups;
