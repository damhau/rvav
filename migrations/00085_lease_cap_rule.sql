-- +goose Up
-- The administrators' lease rule that held a project admin's rule when it
-- decided a deployment's lease (docs/adr/0048-lease-rules-and-lifetime.md):
-- the deployment keeps the stricter of the two, and this one's terms once
-- the project admin's rule is gone. Deployments made before have none.
alter table deployments add column lease_cap_rule_id text references lease_rules on delete set null;
create index deployments_lease_cap_rule_idx on deployments (lease_cap_rule_id) where lease_cap_rule_id is not null;

-- +goose Down
drop index deployments_lease_cap_rule_idx;
alter table deployments drop column lease_cap_rule_id;
