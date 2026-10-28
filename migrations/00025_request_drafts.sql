-- +goose Up
-- Requests saved to finish later: the caller's only, unchecked, without
-- the values of secret fields.
create table request_drafts (
    id         text primary key,
    user_id    text not null references users on delete cascade,
    item_id    text not null references catalog_items on delete cascade,
    project_id text not null references projects on delete cascade,
    name       text not null,
    draft      jsonb not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);
create index request_drafts_user_idx on request_drafts (user_id, updated_at desc);

-- +goose Down
drop table request_drafts;
