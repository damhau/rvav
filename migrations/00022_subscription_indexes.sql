-- +goose Up
-- Events by request (the latest pre event of a request, and requests
-- deleted before their workflow started), and runs by event (pruning).
create index integration_events_request_idx on integration_events (request_id, topic, seq);
create index subscription_runs_event_idx on subscription_runs (event_id);

-- +goose Down
drop index subscription_runs_event_idx;
drop index integration_events_request_idx;
