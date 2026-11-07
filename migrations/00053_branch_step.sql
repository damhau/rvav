-- +goose Up
-- A composite step can choose between sides (ADR-0033): its row keeps which it
-- took, and the timeline says it.
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest', 'person', 'wait', 'branch'));

-- +goose Down
update request_steps set system = 'rvav' where system = 'branch';
alter table request_steps drop constraint request_steps_system_check;
alter table request_steps add constraint request_steps_system_check check (system in ('rvav', 'policy', 'vcenter', 'efficientip', 'awx', 'nsx', 'rest', 'person', 'wait'));
