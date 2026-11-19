package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// VMRow is a deployment's VM with its deployment, project and the
// addresses rvav reserved for it.
type VMRow struct {
	Resource
	DeploymentName   string   `db:"deployment_name"`
	DeploymentStatus string   `db:"deployment_status"`
	Environment      string   `db:"environment"`
	ProjectID        string   `db:"project_id"`
	ProjectCode      string   `db:"project_code"`
	ProjectName      string   `db:"project_name"`
	Reserved         []string `db:"reserved"`
}

type VMFilter struct {
	Projects []string // nil means all projects
	// OwnOnly and Viewer narrow Projects to what a person sees (Scope).
	OwnOnly      []string
	Viewer       string
	ProjectID    string
	Environments []string
	PowerStates  []string
	VCenter      string
	Cluster      string
	Network      string
	Search       string
	Sort         string // a key of vmSorts, descending with a leading "-"
	Limit        int
	After        *Cursor
}

// vmNICs is a VM's adapters as a JSON array, empty when it has none.
const vmNICs = `(case when jsonb_typeof(r.properties->'vm'->'nics') = 'array' then r.properties->'vm'->'nics' else '[]' end)`

// vmReserved are the addresses reserved for the VM r: its deployment's
// EfficientIP addresses with its name or FQDN, IPv4 first.
const vmReserved = `(select a.properties->>'address' from resources a where a.deployment_id = r.deployment_id
	and a.type = 'ipam.address' and (a.properties->>'hostname' = r.name or a.properties->>'fqdn' = r.properties->'vm'->>'fqdn'))`

var vmSorts = map[string]string{
	"name":        "r.name",
	"deployment":  "d.name",
	"project":     "p.name",
	"environment": "d.environment",
	"power_state": "coalesce(r.properties->'vm'->>'power_state', '')",
	"cpu_count":   "lpad(coalesce(r.properties->'vm'->>'cpu_count', '0'), 6, '0')",
	"memory_mb":   "lpad(coalesce(r.properties->'vm'->>'memory_mb', '0'), 10, '0')",
	"disk_gb": `lpad((select coalesce(sum((x->>'capacity_gb')::bigint), 0) from jsonb_array_elements(
		case when jsonb_typeof(r.properties->'vm'->'disks') = 'array' then r.properties->'vm'->'disks' else '[]' end) x)::text,
		12, '0')`,
}

// vmScope is the VMs of a Scope, with its own-only projects and viewer in
// these parameters.
func vmScope(own, viewer int) string {
	return fmt.Sprintf(`from resources r join deployments d on d.id = r.deployment_id join projects p on p.id = d.project_id
	where r.type = 'vsphere.vm' and d.status <> 'deleted' and ($3 = '' or d.project_id = $3)
	and ((($1 or d.project_id = any($2)) and d.project_id <> all($%d::text[])) or d.owner_id = $%d)`, own, viewer)
}

// ListVMs lists the VMs of deployments that aren't deleted, filtered and
// sorted, with keyset pagination.
func (s *Store) ListVMs(ctx context.Context, f VMFilter) (Page[VMRow], error) {
	key, byName, desc := sortOf(vmSorts, f.Sort, "name")
	then := "''"
	if byName {
		then = "r.name"
	}
	after := Cursor{}
	if f.After != nil {
		after = *f.After
	}
	where, order := keyset(desc, 10)
	q := fmt.Sprintf(`select * from (select r.id, r.deployment_id, r.request_id, r.type, r.name, r.external_id, r.sync_state,
			r.properties, r.prevent_delete, d.name as deployment_name, d.status as deployment_status, d.environment,
			p.id as project_id, p.code as project_code, p.name as project_name,
			array(select x.address from (%s) x(address) where x.address is not null
				order by strpos(x.address, ':') > 0, x.address) as reserved,
			%s as sort_key, %s as then_key
		%s
		  and (cardinality($4::text[]) = 0 or d.environment = any($4))
		  and (cardinality($5::text[]) = 0 or r.properties->'vm'->>'power_state' = any($5))
		  and ($6 = '' or r.properties->'vm'->>'vcenter' = $6)
		  and ($7 = '' or r.properties->'vm'->>'cluster' = $7)
		  and ($8 = '' or exists (select 1 from jsonb_array_elements(%s) n where n->>'network' = $8))
		  and ($9 = '' or r.name ilike $9 or r.properties->'vm'->>'fqdn' ilike $9
		       or exists (select 1 from jsonb_array_elements(%s) n,
		             jsonb_array_elements_text(case when jsonb_typeof(n->'ip_addresses') = 'array' then n->'ip_addresses' else '[]' end) ip
		           where ip ilike $9)
		       or exists (select 1 from (%s) x(address) where x.address ilike $9))
		) v
		where %s %s limit $13`,
		vmReserved, key, then, vmScope(14, 15), vmNICs, vmNICs, vmReserved, where, order)
	rows, err := s.Pool.Query(ctx, q, f.Projects == nil, f.Projects, f.ProjectID, nonNil(f.Environments),
		nonNil(f.PowerStates), f.VCenter, f.Cluster, f.Network, SearchText(f.Search), after.Value, after.Then, after.ID, f.Limit+1,
		nonNil(f.OwnOnly), f.Viewer)
	if err != nil {
		return Page[VMRow]{}, err
	}
	type withKey struct {
		VMRow
		SortKey string `db:"sort_key"`
		ThenKey string `db:"then_key"`
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[withKey])
	if err != nil {
		return Page[VMRow]{}, err
	}
	page := paginate(list, f.Limit, func(w withKey) Cursor { return Cursor{Value: w.SortKey, Then: w.ThenKey, ID: w.ID} })
	out := Page[VMRow]{Next: page.Next, Items: make([]VMRow, len(page.Items))}
	for i, w := range page.Items {
		out.Items[i] = w.VMRow
	}
	return out, nil
}

// VMFacets are the vCenters with their clusters, and the networks, of the
// VMs ListVMs would list in these projects.
type VMFacets struct {
	Clusters map[string][]string // by vCenter
	Networks []string
}

func (s *Store) VMFacets(ctx context.Context, sc Scope, projectID string) (VMFacets, error) {
	out := VMFacets{Clusters: map[string][]string{}, Networks: []string{}}
	rows, _ := s.Pool.Query(ctx, `select distinct r.properties->'vm'->>'vcenter' as vcenter,
		coalesce(r.properties->'vm'->>'cluster', '') as cluster `+vmScope(4, 5)+`
		and coalesce(r.properties->'vm'->>'vcenter', '') <> '' order by 1, 2`, sc.args(projectID)...)
	type placed struct {
		VCenter string `db:"vcenter"`
		Cluster string `db:"cluster"`
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[placed])
	if err != nil {
		return out, err
	}
	for _, p := range list {
		clusters := out.Clusters[p.VCenter]
		if clusters == nil {
			clusters = []string{}
		}
		if p.Cluster != "" {
			clusters = append(clusters, p.Cluster)
		}
		out.Clusters[p.VCenter] = clusters
	}
	out.Networks, err = s.strings(ctx, `select distinct n->>'network' from resources r,
		jsonb_array_elements(`+vmNICs+`) n
		where r.id in (select r.id `+vmScope(4, 5)+`) and coalesce(n->>'network', '') <> '' order by 1`, sc.args(projectID)...)
	return out, err
}

// DeploymentFacets are the catalog items, VM templates and environments
// of the deployments in these projects that aren't deleted.
type DeploymentFacets struct {
	Items        []FacetItem
	Templates    []string
	Environments []string
}

type FacetItem struct {
	ID   string `db:"id"`
	Slug string `db:"slug"`
	Name string `db:"name"`
}

const deploymentScope = `from deployments d where d.status <> 'deleted' and ($3 = '' or d.project_id = $3)
	and ((($1 or d.project_id = any($2)) and d.project_id <> all($4::text[])) or d.owner_id = $5)`

// Scope is the deployments a person sees (ADR-0045): those of Projects (nil
// for every project) but OwnOnly's, where they see their own only, and
// their own wherever they are.
type Scope struct {
	Projects []string
	OwnOnly  []string
	Viewer   string
}

func (sc Scope) args(projectID string) []any {
	return []any{sc.Projects == nil, sc.Projects, projectID, nonNil(sc.OwnOnly), sc.Viewer}
}

func (s *Store) DeploymentFacets(ctx context.Context, sc Scope, projectID string) (DeploymentFacets, error) {
	var out DeploymentFacets
	rows, _ := s.Pool.Query(ctx, `select i.id, i.slug, i.name from catalog_items i
		where i.id in (select d.item_id `+deploymentScope+`) order by i.name, i.id`, sc.args(projectID)...)
	items, err := pgx.CollectRows(rows, pgx.RowToStructByName[FacetItem])
	if err != nil {
		return out, err
	}
	out.Items = items
	if out.Templates, err = s.strings(ctx, `select distinct r.properties->'vm'->>'template' from resources r
		where r.type = 'vsphere.vm' and coalesce(r.properties->'vm'->>'template', '') <> ''
		and r.deployment_id in (select d.id `+deploymentScope+`) order by 1`, sc.args(projectID)...); err != nil {
		return out, err
	}
	out.Environments, err = s.strings(ctx, `select distinct d.environment `+deploymentScope+` order by 1`, sc.args(projectID)...)
	return out, err
}

func (s *Store) strings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, q, args...)
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if out == nil {
		out = []string{}
	}
	return out, err
}
