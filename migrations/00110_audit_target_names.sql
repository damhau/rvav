-- +goose Up
-- What an audit event's target was called when it was recorded, so the log
-- still names a target renamed or deleted since. Older events are named at
-- read time from the target as it is now (internal/store/audit_targets.go).
alter table audit_events add column target_name text;

-- +goose Down
alter table audit_events drop column target_name;
