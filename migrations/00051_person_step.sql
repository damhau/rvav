-- +goose Up
-- A composite step can wait for a person (ADR-0033): the wait is an approval
-- row, with its step, its form and the answer it got.
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest', 'person'));
alter table approvals
    add column step_key text,
    add column message text not null default '',
    add column form jsonb,
    add column answer jsonb,
    add column retired boolean not null default false;
-- One question open or answered per step of a run: a retried activity finds
-- it; a person's Retry retires it, so the step asks again.
create unique index approvals_step_open on approvals (request_id, step_key)
    where step_key is not null and not retired and status <> 'cancelled';

-- +goose Down
delete from approvals where step_key is not null;
drop index approvals_step_open;
alter table approvals
    drop column step_key,
    drop column message,
    drop column form,
    drop column answer,
    drop column retired;
update request_steps set system = 'rvav' where system = 'person';
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest'));
