-- +goose Up
-- ServiceNow names people by email or by user name (docs/adr/0065-servicenow.md):
-- a user's user name is their identity provider's preferred_username at
-- their last sign-in. A request from ServiceNow keeps who ordered it,
-- next to the user it's for.
alter table users add column username text not null default '';
create index users_username_idx on users (lower(username)) where username <> '';

alter table servicenow_requests
    -- Set when someone else than the user placed the order.
    add column ordered_by_id    text references users,
    -- The email or user name ServiceNow named the orderer by.
    add column ordered_by_given text not null default '';

-- +goose Down
alter table servicenow_requests drop column ordered_by_given, drop column ordered_by_id;
drop index users_username_idx;
alter table users drop column username;
