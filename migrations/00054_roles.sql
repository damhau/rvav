-- +goose Up
-- Custom roles (ADR-0034): roles made of permissions. The built-in ones are
-- listed with none, since theirs are code, so that every assignment
-- references a role by its key.
create table roles (
    key          text primary key check (key ~ '^[a-z][a-z0-9-]{1,40}$'),
    name         text not null,
    description  text not null default '',
    permissions  text[] not null default '{}',
    built_in     boolean not null default false,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now()
);
insert into roles (key, name, built_in) values
    ('admin', 'Administrator', true), ('catalog-admin', 'Catalog admin', true), ('auditor', 'Auditor', true),
    ('user', 'User', true), ('project-admin', 'Project admin', true), ('member', 'Member', true), ('viewer', 'Viewer', true);

-- A user's roles for all of rvav, given in the UI. users.global_roles keeps
-- what rvav sets itself (user, the local administrator's admin, the system
-- users'), which the API never changes.
create table user_roles (
    id          text primary key,
    user_id     text not null references users on delete cascade,
    role        text not null references roles,
    created_at  timestamptz not null default now(),
    unique (user_id, role)
);

-- Several roles per user and project, any role by its key.
alter table project_members drop constraint project_members_role_check;
alter table project_members drop constraint project_members_pkey;
alter table project_members add primary key (project_id, user_id, role);
alter table project_members add constraint project_members_role_fkey foreign key (role) references roles;
create index project_members_user_idx on project_members (user_id);

-- A mapping to a name that is no role granted nothing: it goes.
delete from group_mappings where role not in (select key from roles);
alter table group_mappings add constraint group_mappings_role_fkey foreign key (role) references roles;

-- +goose Down
delete from group_mappings where role in (select key from roles where not built_in);
alter table group_mappings drop constraint group_mappings_role_fkey;

-- One role per user and project again: the strongest built-in one.
delete from project_members m
where array_position(array['viewer', 'member', 'project-admin'], m.role) is null
   or exists (select 1 from project_members o where o.project_id = m.project_id and o.user_id = m.user_id
              and array_position(array['viewer', 'member', 'project-admin'], o.role) >
                  array_position(array['viewer', 'member', 'project-admin'], m.role));
drop index project_members_user_idx;
alter table project_members drop constraint project_members_role_fkey;
alter table project_members drop constraint project_members_pkey;
alter table project_members add primary key (project_id, user_id);
alter table project_members add constraint project_members_role_check check (role in ('project-admin', 'member', 'viewer'));

drop table user_roles;
drop table roles;
