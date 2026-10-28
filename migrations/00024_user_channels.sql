-- +goose Up
-- Who a user is on Teams and Slack (docs/adr/0023-notifications.md): found
-- by their email when rvav first sends them something there, and how a
-- button pressed there is matched to them.
create table user_channels (
    user_id         text not null references users on delete cascade,
    channel         text not null check (channel in ('teams', 'slack')),
    external_id     text not null,
    -- Teams: the one-to-one conversation with rvav's bot; Slack: the DM.
    conversation_id text,
    updated_at      timestamptz not null default now(),
    primary key (user_id, channel),
    unique (channel, external_id)
);

-- +goose Down
drop table user_channels;
