-- +goose Up
-- What a composite item's step gave (docs/adr/0022-custom-workflows-and-composites.md):
-- later steps bind their inputs to it, and a retried request finds it again.
alter table request_steps add column outputs jsonb;

-- +goose Down
alter table request_steps drop column outputs;
