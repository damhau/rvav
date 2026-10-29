-- +goose Up
-- When a custom workflow's approval decision reached it (ADR-0022): one
-- whose signal failed is sent again until it does (#81).
alter table approvals add column signalled_at timestamptz;
update approvals set signalled_at = coalesce(decided_at, now()) where workflow_id is not null and status <> 'pending';

-- +goose Down
alter table approvals drop column signalled_at;
