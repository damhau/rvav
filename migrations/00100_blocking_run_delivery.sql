-- +goose Up
-- The workflow activity delivering a blocking run: when Temporal tries it
-- again after an infrastructure failure, the run it finished stands and its
-- target isn't asked again (ADR-0054).
alter table subscription_runs add column delivery text;

-- +goose Down
alter table subscription_runs drop column delivery;
