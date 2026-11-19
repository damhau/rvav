-- +goose Up
-- The built-in supervisor (ADR-0041). A custom role made with its key
-- before keeps its holders and the day-2 policies naming it, as
-- supervisor-custom.
insert into roles (key, name, description, permissions, created_at)
select 'supervisor-custom', name, description, permissions, created_at from roles where key = 'supervisor' and not built_in;
update project_members set role = 'supervisor-custom'
where role = 'supervisor' and exists (select 1 from roles where key = 'supervisor-custom');
update group_mappings set role = 'supervisor-custom'
where role = 'supervisor' and exists (select 1 from roles where key = 'supervisor-custom');
update user_roles set role = 'supervisor-custom'
where role = 'supervisor' and exists (select 1 from roles where key = 'supervisor-custom');
update day2_policies set roles = array_replace(roles, 'supervisor', 'supervisor-custom')
where 'supervisor' = any(roles) and exists (select 1 from roles where key = 'supervisor-custom');
delete from roles where key = 'supervisor' and not built_in;
insert into roles (key, name, built_in) values ('supervisor', 'Supervisor', true);

-- +goose Down
delete from project_members where role = 'supervisor';
delete from group_mappings where role = 'supervisor';
delete from user_roles where role = 'supervisor';
-- A day-2 policy naming it keeps the key, which matches nobody now:
-- removing it would make the policy apply to everyone.
delete from roles where key = 'supervisor';
