-- +goose Up
-- A request's request.submitted event is held until its workflow has
-- started: subscribers never hear of a request Temporal refused, which is
-- then removed (#81). Held events are not dispatched.
alter table integration_events add column held boolean not null default false;

-- +goose Down
alter table integration_events drop column held;
