-- +goose Up
-- A composite step can wait for a time or a condition (ADR-0033): until when
-- is kept on the step, so a run continued as new and a worker back after a
-- while find it, and the timeline says it.
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest', 'person', 'wait'));
alter table request_steps add column due_at timestamptz;

-- +goose Down
alter table request_steps drop column due_at;
update request_steps set system = 'rvav' where system = 'wait';
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest', 'person'));
