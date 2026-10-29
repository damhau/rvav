-- +goose Up
-- Console tokens (docs/adr/0012-vm-console.md) in the database, so that
-- the console's WebSocket may reach any API replica (S14.1). A token is
-- kept as its SHA-256; it works once, for a minute.
create table console_tokens (
    token_hash  bytea primary key,
    ticket      jsonb not null,
    expires_at  timestamptz not null
);

-- +goose Down
drop table console_tokens;
