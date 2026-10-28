package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Placement rules and network mappings set by hand (ADR-0020).

// PlacementRuleRow is a rule with its project's code and name.
type PlacementRuleRow struct {
	PlacementRule
	ProjectCode string `db:"project_code"`
	ProjectName string `db:"project_name"`
}

const placementRuleRowSelect = `select r.id, r.project_id, r.environment, r.priority, r.cluster_patterns, r.datastore_patterns,
	r.network_patterns, r.folder, r.cluster_tags, r.datastore_tags, p.code as project_code, p.name as project_name
	from placement_rules r join projects p on p.id = r.project_id`

// AllPlacementRules lists the rules of a project, or of all ("" for all).
func (s *Store) AllPlacementRules(ctx context.Context, projectID string) ([]PlacementRuleRow, error) {
	rows, _ := s.Pool.Query(ctx, placementRuleRowSelect+` where $1 = '' or r.project_id = $1
		order by p.name, r.environment, r.priority, r.id`, projectID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[PlacementRuleRow])
}

func (s *Store) GetPlacementRule(ctx context.Context, id string) (PlacementRuleRow, error) {
	rows, _ := s.Pool.Query(ctx, placementRuleRowSelect+` where r.id = $1`, id)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[PlacementRuleRow])
	return r, notFound(err)
}

// SavePlacementRule creates the rule or replaces it.
func (s *Store) SavePlacementRule(ctx context.Context, r PlacementRule) error {
	_, err := s.Pool.Exec(ctx, `insert into placement_rules (id, project_id, environment, priority, cluster_patterns,
		datastore_patterns, network_patterns, folder, cluster_tags, datastore_tags) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		on conflict (id) do update set project_id = excluded.project_id, environment = excluded.environment,
		priority = excluded.priority, cluster_patterns = excluded.cluster_patterns, datastore_patterns = excluded.datastore_patterns,
		network_patterns = excluded.network_patterns, folder = excluded.folder, cluster_tags = excluded.cluster_tags,
		datastore_tags = excluded.datastore_tags`,
		r.ID, r.ProjectID, r.Environment, r.Priority, nonNil(r.ClusterPatterns), nonNil(r.DatastorePatterns),
		nonNil(r.NetworkPatterns), r.Folder, nonNil(r.ClusterTags), nonNil(r.DatastoreTags))
	return err
}

func (s *Store) DeletePlacementRule(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from placement_rules where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// NetworkMappings lists every network's subnet, by network.
func (s *Store) NetworkMappings(ctx context.Context) ([]NetworkSubnet, error) {
	rows, _ := s.Pool.Query(ctx, `select `+subnetColumns+` from network_subnets order by network`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[NetworkSubnet])
}

// SetManualMapping sets a network's subnet by hand; the EfficientIP sync
// leaves it alone from then on.
func (s *Store) SetManualMapping(ctx context.Context, n NetworkSubnet) error {
	_, err := s.Pool.Exec(ctx, `insert into network_subnets (network, subnet, gateway, dns_zone, dns_servers, source)
		values ($1, $2, $3, $4, $5, 'manual')
		on conflict (network) do update set subnet = excluded.subnet, gateway = excluded.gateway, dns_zone = excluded.dns_zone,
		dns_servers = excluded.dns_servers, source = 'manual'`, n.Network, n.Subnet, n.Gateway, n.DNSZone, nonNil(n.DNSServers))
	return err
}

// DeleteManualMapping removes a mapping set by hand (ErrNotFound when the
// network has none); the next sync brings EfficientIP's back.
func (s *Store) DeleteManualMapping(ctx context.Context, network string) error {
	tag, err := s.Pool.Exec(ctx, `delete from network_subnets where network = $1 and source = 'manual'`, network)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
