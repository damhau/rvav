-- +goose Up
-- Segments made for a deployment (ADR-0028): the Tier-1 they connect to,
-- the EfficientIP block their subnets are carved from, and the DNS of the
-- VMs on them. Null: the rule places nothing that makes segments.
alter table placement_rules add column segments jsonb;

-- +goose Down
alter table placement_rules drop column segments;
