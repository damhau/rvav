-- +goose Up
-- The catalog in Git (docs/adr/0029-catalog-in-git.md): each item's file in
-- the repository, what rvav waits to commit to it, and what the repository
-- proposed.
create table catalog_git_files (
    path              text primary key,
    -- Null for a file no item came from (a new file the import refused), or
    -- for an item deleted in rvav whose file waits to be removed.
    item_id           text unique references catalog_items on delete set null,
    -- What rvav made of the repository's file: synced (both agree, or
    -- rvav's commit waits), draft (a draft was made of it), conflict (both
    -- changed it), refused (the import refused it), deleted (gone from the
    -- repository).
    state             text not null default 'synced'
                      check (state in ('synced', 'draft', 'conflict', 'refused', 'deleted')),
    -- The file's blob rvav has accounted for: another is a change.
    seen_blob         text,
    -- The file as the repository has it, when rvav didn't take it.
    repo_blob         text,
    repo_content      text,
    problems          jsonb,
    note              text not null default '',
    -- The last commit that changed the file.
    last_commit       jsonb,
    draft_version_id  text references catalog_item_versions on delete set null,
    draft_revision    int,
    -- rvav's change waiting to be committed: who made it, and the message.
    pending           boolean not null default false,
    remove            boolean not null default false,
    pending_by        text references users on delete set null,
    pending_message   text not null default '',
    pending_at        timestamptz,
    revision          bigint not null default 0,
    attempts          int not null default 0,
    next_attempt_at   timestamptz,
    error             text,
    updated_at        timestamptz not null default now()
);

create table catalog_git_repo (
    id                 boolean primary key default true check (id),
    -- The repository, branch and directory read: another starts afresh.
    identity           text not null,
    head               text,
    read_at            timestamptz,
    error              text,
    read_requested_at  timestamptz
);

-- +goose Down
drop table catalog_git_repo;
drop table catalog_git_files;
