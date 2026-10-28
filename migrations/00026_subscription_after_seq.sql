-- +goose Up
-- A subscription gets the events written after it was saved: the outbox
-- sequence when it was, rather than a timestamp, which a clock set back
-- would make wrong (docs/adr/0021-event-subscriptions.md).
alter table subscriptions add column after_seq bigint not null default 0;
update subscriptions s set after_seq = coalesce((select max(seq) from integration_events e where e.created_at < s.created_at), 0);

-- +goose Down
alter table subscriptions drop column after_seq;
