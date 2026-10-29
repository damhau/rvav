package core

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// Placement rules, their preview and network mappings set by hand
// (ADR-0020).

func presentPlacementRule(r store.PlacementRuleRow) api.PlacementRule {
	return api.PlacementRule{Id: r.ID, Project: api.ProjectRef{Id: r.ProjectID, Code: r.ProjectCode, Name: r.ProjectName},
		Environment: r.Environment, Priority: r.Priority, Clusters: nonNilStrings(r.ClusterPatterns),
		Datastores: nonNilStrings(r.DatastorePatterns), Networks: nonNilStrings(r.NetworkPatterns), Folder: r.Folder,
		ClusterTags: nonNilStrings(r.ClusterTags), DatastoreTags: nonNilStrings(r.DatastoreTags), StoragePolicy: r.StoragePolicy}
}

// canSeeSettings: administrators and auditors everywhere; a project's
// admins for their own project.
func canSeeSettings(p *auth.Principal, projectID string) bool {
	return p.SeesEverything() || projectID != "" && p.ProjectRole(projectID) == "project-admin"
}

func (s *Service) PlacementRules(ctx context.Context, p *auth.Principal, projectID string) ([]api.PlacementRule, error) {
	if projectID != "" {
		proj, err := s.visibleProject(ctx, p, projectID)
		if err != nil {
			return nil, err
		}
		projectID = proj.ID
	}
	if !canSeeSettings(p, projectID) {
		return nil, ErrForbidden
	}
	list, err := s.Store.AllPlacementRules(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]api.PlacementRule, len(list))
	for i, r := range list {
		out[i] = presentPlacementRule(r)
	}
	return out, nil
}

// checkPatterns says which patterns aren't valid globs.
func checkPatterns(list []string, ptr string) []api.FieldError {
	var errs []api.FieldError
	for i, p := range list {
		if _, err := path.Match(p, ""); err != nil || strings.TrimSpace(p) == "" {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/%d", ptr, i), "pattern", fmt.Sprintf("%q is not a valid pattern.", p)))
		}
	}
	return errs
}

func checkTags(list []string, ptr string) []api.FieldError {
	var errs []api.FieldError
	for i, t := range list {
		if cat, tag, ok := strings.Cut(t, "/"); !ok || cat == "" || tag == "" {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/%d", ptr, i), "pattern", fmt.Sprintf("%q is not category/tag.", t)))
		}
	}
	return errs
}

func (s *Service) SavePlacementRule(ctx context.Context, p *auth.Principal, id string, w api.PlacementRuleWrite) (api.PlacementRule, error) {
	if !p.IsAdmin() {
		return api.PlacementRule{}, ErrForbidden
	}
	if id != "" {
		if _, err := s.Store.GetPlacementRule(ctx, id); errors.Is(err, store.ErrNotFound) {
			return api.PlacementRule{}, ErrNotFound
		} else if err != nil {
			return api.PlacementRule{}, err
		}
	}
	clusterTags, datastoreTags := derefSlice(w.ClusterTags), derefSlice(w.DatastoreTags)
	var errs []api.FieldError
	proj, err := s.Store.GetProject(ctx, w.ProjectId)
	switch {
	case errors.Is(err, store.ErrNotFound):
		errs = append(errs, fieldErr("/project_id", "unknown", "There is no such project."))
	case err != nil:
		return api.PlacementRule{}, err
	case !slices.Contains(proj.Environments, w.Environment):
		errs = append(errs, fieldErr("/environment", "not_allowed", fmt.Sprintf("%s doesn't deploy to %s.", proj.Name, w.Environment)))
	}
	if len(w.Clusters) == 0 && len(clusterTags) == 0 {
		errs = append(errs, fieldErr("/clusters", "required", "Name clusters (patterns such as GVA-DEV-*), or tags they carry."))
	}
	if len(w.Datastores) == 0 && len(datastoreTags) == 0 {
		errs = append(errs, fieldErr("/datastores", "required", "Name datastores, or tags they carry."))
	}
	if len(w.Networks) == 0 {
		errs = append(errs, fieldErr("/networks", "required", "Name the networks VMs may connect to."))
	}
	if !strings.HasPrefix(w.Folder, "/") {
		errs = append(errs, fieldErr("/folder", "pattern", "A folder's path, for example /GVA/vm/rvav/web/dev."))
	}
	errs = append(errs, checkPatterns(w.Clusters, "/clusters")...)
	errs = append(errs, checkPatterns(w.Datastores, "/datastores")...)
	errs = append(errs, checkPatterns(w.Networks, "/networks")...)
	errs = append(errs, checkTags(clusterTags, "/cluster_tags")...)
	errs = append(errs, checkTags(datastoreTags, "/datastore_tags")...)
	// A storage policy vCenter has (ADR-0024), when the inventory knows them.
	policy := strings.TrimSpace(deref(w.StoragePolicy))
	if policy != "" {
		policies, err := s.Store.InventoryStoragePolicies(ctx)
		if err != nil {
			return api.PlacementRule{}, err
		}
		if len(policies) > 0 && !slices.ContainsFunc(policies, func(p store.InvStoragePolicy) bool { return p.Name == policy }) {
			errs = append(errs, fieldErr("/storage_policy", "unknown", "vCenter has no storage policy "+policy+"."))
		}
	}
	if len(errs) > 0 {
		return api.PlacementRule{}, &ValidationError{Errors: errs}
	}
	action := "placement.rule.update"
	if id == "" {
		id, action = ids.New("plc"), "placement.rule.create"
	}
	priority := 100
	if w.Priority != nil {
		priority = *w.Priority
	}
	r := store.PlacementRule{ID: id, ProjectID: proj.ID, Environment: w.Environment, Priority: priority,
		ClusterPatterns: w.Clusters, DatastorePatterns: w.Datastores, NetworkPatterns: w.Networks, Folder: w.Folder,
		ClusterTags: clusterTags, DatastoreTags: datastoreTags, StoragePolicy: policy}
	if err := s.Store.SavePlacementRule(ctx, r); err != nil {
		return api.PlacementRule{}, err
	}
	s.audit(ctx, p, action, "placement_rule", id, &proj.ID, w)
	row, err := s.Store.GetPlacementRule(ctx, id)
	return presentPlacementRule(row), err
}

func (s *Service) DeletePlacementRule(ctx context.Context, p *auth.Principal, id string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	r, err := s.Store.GetPlacementRule(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := s.Store.DeletePlacementRule(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, p, "placement.rule.delete", "placement_rule", id, &r.ProjectID, nil)
	return nil
}

// PlacementPreview is what a project and environment can use now, and
// what stops a request there.
func (s *Service) PlacementPreview(ctx context.Context, p *auth.Principal, projectID, environment string) (api.PlacementPreview, error) {
	out := api.PlacementPreview{Clusters: []api.PreviewCluster{}, Datastores: []api.PreviewDatastore{},
		Networks: []api.PreviewNetwork{}, Problems: []string{}}
	proj, err := s.visibleProject(ctx, p, projectID)
	if err != nil {
		return out, err
	}
	if !canSeeSettings(p, proj.ID) {
		return out, ErrForbidden
	}
	pl, err := s.placement(ctx, proj.ID, environment)
	if err != nil {
		return out, err
	}
	problem := func(format string, args ...any) { out.Problems = append(out.Problems, fmt.Sprintf(format, args...)) }
	if !slices.Contains(proj.Environments, environment) {
		problem("%s doesn't deploy to %s.", proj.Name, environment)
	}
	if len(pl.rules) == 0 {
		problem("No placement rule for %s in %s: requests there fail.", proj.Name, environment)
		return out, nil
	}
	// The folder of the rule that places the first cluster: requests use
	// the rule that matched the cluster chosen.
	out.Folder = pl.rules[0].Folder
	if len(pl.clusters) > 0 {
		if r := pl.rule(pl.clusters[0].Name); r != nil {
			out.Folder = r.Folder
		}
	}
	if len(pl.clusters) == 0 {
		problem("No cluster in vCenter matches the rules.")
	}
	datastores := map[string]*api.PreviewDatastore{}
	networks := map[string]*api.PreviewNetwork{}
	for _, c := range pl.clusters {
		out.Clusters = append(out.Clusters, api.PreviewCluster{Name: c.Name, Hosts: c.HostCount, Tags: nonNilStrings(c.Tags)})
		ds := pl.datastoresFor(c.Name)
		if len(ds) == 0 {
			problem("Cluster %s has no datastore the rules allow.", c.Name)
		}
		for _, d := range ds {
			if datastores[d.Name] == nil {
				datastores[d.Name] = &api.PreviewDatastore{Name: d.Name, FreeGb: int(d.FreeGB), Tags: nonNilStrings(d.Tags), Clusters: []string{}}
			}
			datastores[d.Name].Clusters = append(datastores[d.Name].Clusters, c.Name)
		}
		nets := pl.networksFor(c.Name)
		if len(nets) == 0 {
			problem("Cluster %s has no network the rules allow.", c.Name)
		}
		for _, n := range nets {
			if networks[n.Name] == nil {
				pn := &api.PreviewNetwork{Name: n.Name, Clusters: []string{}}
				if sub, ok := pl.subnets[n.Name]; ok {
					src := api.PreviewNetworkSource(sub.Source)
					pn.Subnet, pn.DnsZone, pn.Source = &sub.Subnet, &sub.DNSZone, &src
				} else {
					problem("Network %s has no subnet: map it by hand or in EfficientIP.", n.Name)
				}
				networks[n.Name] = pn
			}
			networks[n.Name].Clusters = append(networks[n.Name].Clusters, c.Name)
		}
	}
	for _, d := range datastores {
		out.Datastores = append(out.Datastores, *d)
	}
	for _, n := range networks {
		out.Networks = append(out.Networks, *n)
	}
	sort.Slice(out.Datastores, func(i, j int) bool { return out.Datastores[i].Name < out.Datastores[j].Name })
	sort.Slice(out.Networks, func(i, j int) bool { return out.Networks[i].Name < out.Networks[j].Name })
	return out, nil
}

func presentNetworkMapping(n store.NetworkSubnet) api.NetworkMapping {
	out := api.NetworkMapping{Network: n.Network, Subnet: n.Subnet, Gateway: n.Gateway, DnsZone: n.DNSZone,
		DnsServers: nonNilStrings(n.DNSServers), Source: api.NetworkMappingSource(n.Source), SyncedAt: n.SyncedAt}
	if n.SubnetName != "" {
		out.SubnetName = &n.SubnetName
	}
	return out
}

func (s *Service) NetworkMappings(ctx context.Context, p *auth.Principal) ([]api.NetworkMapping, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	list, err := s.Store.NetworkMappings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.NetworkMapping, len(list))
	for i, n := range list {
		out[i] = presentNetworkMapping(n)
	}
	return out, nil
}

// SetNetworkMapping sets a network's subnet by hand: it wins over
// EfficientIP's (carry-over from S7.3).
func (s *Service) SetNetworkMapping(ctx context.Context, p *auth.Principal, network string, w api.NetworkMappingWrite) (api.NetworkMapping, error) {
	if !p.IsAdmin() {
		return api.NetworkMapping{}, ErrForbidden
	}
	var errs []api.FieldError
	prefix, err := netip.ParsePrefix(w.Subnet)
	if err != nil || prefix.Masked() != prefix {
		errs = append(errs, fieldErr("/subnet", "invalid", "A subnet such as 10.31.10.0/24."))
	}
	if gw, err := netip.ParseAddr(w.Gateway); err != nil {
		errs = append(errs, fieldErr("/gateway", "invalid", "An IP address."))
	} else if prefix.IsValid() && !prefix.Contains(gw) {
		errs = append(errs, fieldErr("/gateway", "outside", "The gateway must be in the subnet."))
	}
	if strings.TrimSpace(w.DnsZone) == "" {
		errs = append(errs, fieldErr("/dns_zone", "required", "The DNS zone VMs on this network are named in."))
	}
	servers := derefSlice(w.DnsServers)
	for i, d := range servers {
		if _, err := netip.ParseAddr(d); err != nil {
			errs = append(errs, fieldErr(fmt.Sprintf("/dns_servers/%d", i), "invalid", "An IP address."))
		}
	}
	if strings.TrimSpace(network) == "" {
		errs = append(errs, fieldErr("/network", "required", "Name the vSphere network."))
	}
	if len(errs) > 0 {
		return api.NetworkMapping{}, &ValidationError{Errors: errs}
	}
	n := store.NetworkSubnet{Network: network, Subnet: w.Subnet, Gateway: w.Gateway, DNSZone: strings.TrimSpace(w.DnsZone),
		DNSServers: servers, Source: "manual"}
	if err := s.Store.SetManualMapping(ctx, n); err != nil {
		return api.NetworkMapping{}, err
	}
	s.audit(ctx, p, "network.mapping.set", "network", network, nil, w)
	saved, err := s.Store.NetworkSubnet(ctx, network)
	return presentNetworkMapping(saved), err
}

func (s *Service) DeleteNetworkMapping(ctx context.Context, p *auth.Principal, network string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	if err := s.Store.DeleteManualMapping(ctx, network); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "network.mapping.delete", "network", network, nil, nil)
	return nil
}

func derefSlice(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}
