-- +goose Up
-- What each step was given, as it ran with it (docs/adr/0057-run-data-variables-schedules-and-loops.md):
-- with its outputs, a run's data. Secret inputs stay markers, stored secrets names.
alter table request_steps add column inputs jsonb;

-- +goose Down
alter table request_steps drop column if exists inputs;
