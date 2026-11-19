-- +goose Up
-- The requester's manager as an approver (ADR-0041): the manager claim's
-- value at the user's last sign-in, an email or a subject; whether a level
-- asks the manager, and the user it found when it opened.
alter table users add column manager text not null default '';
alter table approvals add column manager boolean not null default false,
    add column manager_id text;

-- +goose Down
alter table approvals drop column manager, drop column manager_id;
alter table users drop column manager;
