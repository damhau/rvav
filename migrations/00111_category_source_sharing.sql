-- +goose Up
-- Sharing a category or a source (ADR-0019, Sharing): each item in it, those
-- added or moved in later included, is shared as it is, on top of its own
-- shares. The columns are an item's: every project with its audience
-- (share_*), and chosen projects with theirs.
alter table catalog_categories
    add column all_projects boolean not null default false,
    add column share_user_ids text[] not null default '{}',
    add column share_groups text[] not null default '{}',
    add column share_roles text[] not null default '{}';

create table catalog_category_entitlements (
    category    text not null references catalog_categories on delete cascade,
    project_id  text not null references projects on delete cascade,
    user_ids    text[] not null default '{}',
    groups      text[] not null default '{}',
    roles       text[] not null default '{}',
    primary key (category, project_id)
);

-- Only AWX's sync is a source that can be shared: rvav's Git repository
-- holds the whole catalog, so a share of it would be a share of everything.
create table catalog_sources (
    kind            text primary key check (kind in ('awx')),
    name            text not null,
    all_projects    boolean not null default false,
    share_user_ids  text[] not null default '{}',
    share_groups    text[] not null default '{}',
    share_roles     text[] not null default '{}'
);

create table catalog_source_entitlements (
    source_kind text not null references catalog_sources on delete cascade,
    project_id  text not null references projects on delete cascade,
    user_ids    text[] not null default '{}',
    groups      text[] not null default '{}',
    roles       text[] not null default '{}',
    primary key (source_kind, project_id)
);

-- The sync opened each item it made to every project: the source is now,
-- and those items whose sharing no administrator set follow it.
insert into catalog_sources (kind, name, all_projects) values ('awx', 'AWX', true);
update catalog_items i set all_projects = false
where i.source_kind = 'awx' and i.all_projects
  and cardinality(i.share_user_ids) + cardinality(i.share_groups) + cardinality(i.share_roles) = 0
  and not exists (select 1 from audit_events a where a.target_type = 'catalog_item' and a.target_id = i.id
                  and a.action = 'catalog.item.entitlements');

-- +goose Down
update catalog_items i set all_projects = true
where i.source_kind = 'awx' and not i.all_projects
  and exists (select 1 from catalog_sources s where s.kind = 'awx' and s.all_projects);
drop table catalog_source_entitlements;
drop table catalog_sources;
drop table catalog_category_entitlements;
alter table catalog_categories drop column all_projects, drop column share_user_ids, drop column share_groups,
    drop column share_roles;
