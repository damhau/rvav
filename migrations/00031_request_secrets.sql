-- +goose Up
-- A request's secret inputs (form fields marked secret), sealed with the
-- secrets key (ADR-0019) like stored secrets: the request's inputs and plan,
-- and so Temporal's history, hold a marker in their place, and the AWX job
-- step opens them only to launch (S14.6).
create table request_secrets (
    -- Sealed in the transaction that inserts the request, before it.
    request_id  text not null references requests on delete cascade deferrable initially deferred,
    field       text not null,
    key_id      text not null,
    nonce       bytea not null,
    ciphertext  bytea not null,
    primary key (request_id, field)
);

-- +goose Down
drop table request_secrets;
