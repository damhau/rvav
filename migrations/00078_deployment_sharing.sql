-- +goose Up
-- Who sees a project's deployments (ADR-0045): every member who sees the
-- project, as before, or only their owners and whoever holds
-- deployments.read_all there.
alter table projects
    add column deployment_sharing text not null default 'all_members'
        check (deployment_sharing in ('all_members', 'owner_only'));
-- Hard refuses or asks for approval, as before; soft warns the requester,
-- who may go on.
alter table day2_policies
    add column enforcement text not null default 'hard' check (enforcement in ('hard', 'soft'));
alter table lease_policies
    add column enforcement text not null default 'hard' check (enforcement in ('hard', 'soft'));

-- +goose Down
alter table lease_policies drop column enforcement;
alter table day2_policies drop column enforcement;
alter table projects drop column deployment_sharing;
