-- +goose Up
-- Who may request an item in a project it is shared with (ADR-0019,
-- Sharing): users, OIDC groups and project roles; all the project's
-- members when they name nobody, as every share made before.
alter table catalog_entitlements
    add column user_ids text[] not null default '{}',
    add column groups text[] not null default '{}',
    add column roles text[] not null default '{}';
-- The same for every project, with all_projects.
alter table catalog_items
    add column share_user_ids text[] not null default '{}',
    add column share_groups text[] not null default '{}',
    add column share_roles text[] not null default '{}';

-- +goose Down
alter table catalog_entitlements drop column user_ids, drop column groups, drop column roles;
alter table catalog_items drop column share_user_ids, drop column share_groups, drop column share_roles;
