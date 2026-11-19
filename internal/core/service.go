package core

import (
	"context"
	"errors"
	"github.com/damhau/rvav/internal/awxcatalog"
	"log/slog"
	"path"
	"slices"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/notify"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

// Workflows starts and controls request workflows (Temporal in production).
type Workflows interface {
	Start(ctx context.Context, requestID string) error
	Cancel(ctx context.Context, requestID string) error
	// Approval signals that an approval level was decided (approved or rejected).
	Approval(ctx context.Context, requestID, approvalID, outcome string) error
	// Rollback deletes what a failed request created.
	Rollback(ctx context.Context, requestID string) error
	// Signal sends a signal to a workflow by its ID (a custom workflow's
	// approval, ADR-0022).
	Signal(ctx context.Context, workflowID, name string, arg any) error
	// StartOnboarding starts a run of an onboarding plan (X1.5).
	StartOnboarding(ctx context.Context, runID string) error
}

type Service struct {
	Store     *store.Store
	IPAM      efficientip.Client
	Workflows Workflows
	Events    *events.Publisher
	// Notifier renders and sends notifications (ADR-0023): the channels set
	// up, and test messages.
	Notifier *notify.Notifier
	Log      *slog.Logger
	// VCenter gives WebMKS tickets for consoles (ADR-0012); it may connect
	// on first use.
	VCenter func(ctx context.Context, name string) (ConsoleSource, error)
	// VSphere connects to a vCenter by name, the integration's for a name
	// rvav doesn't know: onboarding reads the VMs it adopts (S13.3).
	VSphere func(ctx context.Context, name string) (vsphere.Driver, error)
	// ConsoleAccount, when set, is a session of a vCenter account that may
	// only open consoles: VMRC links carry a clone ticket of it. Never
	// rvav's own session, which a clone ticket would hand to the user. Nil
	// with no error when no console account is set now.
	ConsoleAccount func(ctx context.Context) (CloneTicketer, error)
	// AWXCatalog imports AWX templates into the catalog (ADR-0016).
	AWXCatalog *awxcatalog.Sync
	// AWX and its organisation give the awx.inventories and
	// awx.credentials pickers.
	AWX             awx.Client
	AWXOrganization string
	// Secrets seals the secrets value sources and a composite's REST calls
	// use; REST calls the URLs of `rest` value sources (ADR-0019), and says
	// which hosts a composite's REST calls may reach (ADR-0033).
	Secrets *secrets.Box
	REST    *RESTSources
	// Config is the configuration this process started with, saved
	// settings applied (ADR-0020).
	Config config.Config
	// Live reads the settings saved now: those applied on next use follow
	// it (ADR-0030). Without it, Config is used.
	Live *LiveSettings
	// Dispatcher delivers events to subscriptions: their test runs here
	// (ADR-0021).
	Dispatcher *dispatch.Dispatcher
	// GitBeforeCommit runs before each commit to the catalog's repository:
	// tests move the branch with it.
	GitBeforeCommit func(path string)
}

// ConsoleSource is what the web console needs from vCenter.
type ConsoleSource interface {
	Host() string
	WebMKSTicket(ctx context.Context, moref string) (*vsphere.WebMKS, error)
}

// CloneTicketer hands out one-time tickets that clone its session.
type CloneTicketer interface {
	CloneTicket(ctx context.Context) (string, error)
}

// visibleProject loads a project the principal may see.
func (s *Service) visibleProject(ctx context.Context, p *auth.Principal, idOrCode string) (store.Project, error) {
	proj, err := s.Store.GetProject(ctx, idOrCode)
	if errors.Is(err, store.ErrNotFound) {
		return proj, ErrNotFound
	}
	if err != nil {
		return proj, err
	}
	if !p.In(auth.ProjectSee, proj.ID) {
		return proj, ErrNotFound
	}
	return proj, nil
}

// projectFor finds a project by its ID or code for a change that needs a
// permission there. Without the permission the answer is a refusal, whether
// or not the project exists.
func (s *Service) projectFor(ctx context.Context, p *auth.Principal, perm auth.Permission, idOrCode string) (store.Project, error) {
	proj, err := s.Store.GetProject(ctx, idOrCode)
	switch {
	case errors.Is(err, store.ErrNotFound) && !p.Everywhere(perm):
		return proj, ErrForbidden
	case errors.Is(err, store.ErrNotFound):
		return proj, ErrNotFound
	case err != nil:
		return proj, err
	case !p.In(perm, proj.ID):
		return proj, ErrForbidden
	}
	return proj, nil
}

// placement is what a project may use in one environment. Clusters are
// known by name; with several vCenters (ADR-0024) a cluster is the first of
// that name its rules allow, and its datastores and networks are its
// vCenter's.
type placement struct {
	rules      []store.PlacementRule
	all        []store.InvCluster // every cluster of every vCenter
	clusters   []store.InvCluster // those a rule allows
	datastores []store.InvDatastore
	networks   []store.InvNetwork
	subnets    map[string]store.NetworkSubnet
	// hosts, pools and pods are every vCenter's hosts, resource pools and
	// datastore clusters (ADR-0036); a datastore in a pod carries the
	// pod's tags in datastores.
	hosts []store.InvHost
	pools []store.InvResourcePool
	pods  []store.InvDatastoreCluster
	// vms are rvav's VMs per vCenter and cluster, read when a rule spreads
	// them, and planned those the request places, by its components so far.
	vms     map[string]int
	planned map[string]int
	// plannedMB is the memory of the VMs the request places, by cluster.
	plannedMB map[string]int64
	// added are the vCenters added besides the vCenter integration's.
	added vcenterSet
}

// hostCount is how many hosts VMs on the cluster may start on: those
// narrowed, else those that take new VMs, else the cluster's.
func (pl *placement) hostCount(cluster string, narrowed []string) int {
	c := pl.cluster(cluster)
	switch {
	case narrowed != nil:
		return len(narrowed)
	case c != nil && pl.hostsKnown(c):
		return len(pl.usableHosts(c, nil))
	case c != nil:
		return c.HostCount
	}
	return 0
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// selects says whether a rule's patterns and tags choose an object: a name
// matching one of the patterns (no patterns: any name) and every tag
// (ADR-0020).
func selects(patterns, wantTags []string, name string, tags []string) bool {
	if len(patterns) > 0 && !matchAny(patterns, name) {
		return false
	}
	for _, t := range wantTags {
		if !slices.Contains(tags, t) {
			return false
		}
	}
	return len(patterns) > 0 || len(wantTags) > 0
}

// allows says whether a rule allows a cluster: its vCenter, name and tags.
func allows(r store.PlacementRule, c store.InvCluster) bool {
	return (r.VCenter == "" || r.VCenter == c.VCenter) && selects(r.ClusterPatterns, r.ClusterTags, c.Name, c.Tags)
}

// allows says whether a rule allows a cluster, which has the rule's
// resource pool (ADR-0036).
func (pl *placement) allows(r store.PlacementRule, c store.InvCluster) bool {
	return allows(r, c) && (r.ResourcePool == "" || pl.pool(c, r.ResourcePool) != nil)
}

// pool is the cluster's resource pool at that path.
func (pl *placement) pool(c store.InvCluster, path string) *store.InvResourcePool {
	for i, p := range pl.pools {
		if p.VCenter == c.VCenter && p.Cluster == c.Name && p.Path == path {
			return &pl.pools[i]
		}
	}
	return nil
}

// pod is the vCenter's datastore cluster of that name.
func (pl *placement) pod(vcenter, name string) *store.InvDatastoreCluster {
	for i, p := range pl.pods {
		if p.VCenter == vcenter && p.Name == name {
			return &pl.pods[i]
		}
	}
	return nil
}

// inheritPodTags gives the datastores of a datastore cluster its tags too:
// they count for storage constraints (ADR-0036).
func inheritPodTags(datastores []store.InvDatastore, pods []store.InvDatastoreCluster) {
	for _, p := range pods {
		for i, d := range datastores {
			if d.VCenter == p.VCenter && slices.Contains(p.Datastores, d.Name) {
				datastores[i].Tags = slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(d.Tags), p.Tags...))))
			}
		}
	}
}

// usable is whether a host takes new VMs.
func usable(h store.InvHost) bool { return h.State == vsphere.HostConnected }

// hostsKnown says whether the inventory has the cluster's hosts: without
// them, placement goes by the cluster as before.
func (pl *placement) hostsKnown(c *store.InvCluster) bool {
	return slices.ContainsFunc(pl.hosts, func(h store.InvHost) bool { return h.VCenter == c.VCenter && h.Cluster == c.Name })
}

// usableHosts are the cluster's hosts that take new VMs, all of them or
// those matching patterns.
func (pl *placement) usableHosts(c *store.InvCluster, patterns []string) []store.InvHost {
	var out []store.InvHost
	for _, h := range pl.hosts {
		if h.VCenter == c.VCenter && h.Cluster == c.Name && usable(h) && (len(patterns) == 0 || matchAny(patterns, h.Name)) {
			out = append(out, h)
		}
	}
	return out
}

// hostsFor are the hosts the cluster's rule allows VMs on, only the one
// bound when a blueprint binds one; nil when the inventory has none.
func (pl *placement) hostsFor(c *store.InvCluster, bound string) []store.InvHost {
	if !pl.hostsKnown(c) {
		return nil
	}
	var patterns []string
	if r := pl.rule(c.Name); r != nil {
		patterns = r.HostPatterns
	}
	out := pl.usableHosts(c, patterns)
	if bound != "" {
		out = slices.DeleteFunc(out, func(h store.InvHost) bool { return h.Name != bound })
	}
	return nonNilHosts(out)
}

func nonNilHosts(list []store.InvHost) []store.InvHost {
	if list == nil {
		return []store.InvHost{}
	}
	return list
}

func (s *Service) placement(ctx context.Context, projectID, environment string) (*placement, error) {
	rules, err := s.Store.PlacementRules(ctx, projectID, environment)
	if err != nil {
		return nil, err
	}
	pl := &placement{rules: rules}
	if len(rules) == 0 {
		return pl, nil
	}
	if pl.added, err = s.addedVCenters(ctx); err != nil {
		return nil, err
	}
	if pl.all, err = s.Store.InventoryClusters(ctx); err != nil {
		return nil, err
	}
	if pl.pools, err = s.Store.InventoryResourcePools(ctx); err != nil {
		return nil, err
	}
	for _, c := range pl.all {
		if slices.ContainsFunc(rules, func(r store.PlacementRule) bool { return pl.allows(r, c) }) {
			pl.clusters = append(pl.clusters, c)
		}
	}
	if pl.hosts, err = s.Store.InventoryESXiHosts(ctx); err != nil {
		return nil, err
	}
	if pl.datastores, err = s.Store.InventoryDatastores(ctx); err != nil {
		return nil, err
	}
	if pl.pods, err = s.Store.InventoryDatastoreClusters(ctx); err != nil {
		return nil, err
	}
	inheritPodTags(pl.datastores, pl.pods)
	if slices.ContainsFunc(rules, func(r store.PlacementRule) bool { return r.Strategy == store.StrategySpread }) {
		if pl.vms, err = s.Store.RvavVMsPerCluster(ctx); err != nil {
			return nil, err
		}
	}
	if pl.networks, err = s.Store.InventoryNetworks(ctx); err != nil {
		return nil, err
	}
	if pl.subnets, err = s.Store.NetworkSubnets(ctx); err != nil {
		return nil, err
	}
	return pl, nil
}

// cluster is the allowed cluster of that name.
func (pl *placement) cluster(name string) *store.InvCluster {
	for i := range pl.clusters {
		if pl.clusters[i].Name == name {
			return &pl.clusters[i]
		}
	}
	return nil
}

// ambiguous says whether clusters of that name are allowed in two vCenters:
// a rule allowing it must name the vCenter.
func (pl *placement) ambiguous(name string) bool {
	seen := ""
	for _, c := range pl.clusters {
		if c.Name == name {
			if seen != "" && seen != c.VCenter {
				return true
			}
			seen = c.VCenter
		}
	}
	return false
}

// rule returns the first rule allowing the cluster.
func (pl *placement) rule(cluster string) *store.PlacementRule {
	c := pl.cluster(cluster)
	if c == nil {
		return nil
	}
	for i := range pl.rules {
		if pl.allows(pl.rules[i], *c) {
			return &pl.rules[i]
		}
	}
	return nil
}

// datastoresFor are the datastores the cluster's rule allows: those of
// its datastore cluster, if it names one, narrowed by its patterns and
// tags when it has any (ADR-0036).
func (pl *placement) datastoresFor(cluster string) []store.InvDatastore {
	c, r := pl.cluster(cluster), pl.rule(cluster)
	if r == nil {
		return nil
	}
	var members []string
	if r.DatastoreCluster != "" {
		pod := pl.pod(c.VCenter, r.DatastoreCluster)
		if pod == nil {
			return nil
		}
		members = pod.Datastores
	}
	var out []store.InvDatastore
	for _, d := range pl.datastores {
		if d.VCenter != c.VCenter || !slices.Contains(d.Clusters, cluster) {
			continue
		}
		switch {
		case r.DatastoreCluster == "":
			if selects(r.DatastorePatterns, r.DatastoreTags, d.Name, d.Tags) {
				out = append(out, d)
			}
		case !slices.Contains(members, d.Name):
		case (len(r.DatastorePatterns) == 0 && len(r.DatastoreTags) == 0) || selects(r.DatastorePatterns, r.DatastoreTags, d.Name, d.Tags):
			out = append(out, d)
		}
	}
	return out
}

// datastoresIn are the datastores the cluster's rule allows, only those of
// a datastore cluster when one is named ("" for any).
func (pl *placement) datastoresIn(cluster, pod string) []store.InvDatastore {
	list := pl.datastoresFor(cluster)
	if pod == "" {
		return list
	}
	c := pl.cluster(cluster)
	p := pl.pod(c.VCenter, pod)
	return slices.DeleteFunc(list, func(d store.InvDatastore) bool { return p == nil || !slices.Contains(p.Datastores, d.Name) })
}

// podOf is the datastore cluster a VM's datastore is in, when its rule or
// blueprint names it ("" otherwise).
func (pl *placement) podOf(cluster, bound, datastore string) string {
	name := bound
	if r := pl.rule(cluster); name == "" && r != nil {
		name = r.DatastoreCluster
	}
	c := pl.cluster(cluster)
	if name == "" || c == nil {
		return ""
	}
	if p := pl.pod(c.VCenter, name); p != nil && slices.Contains(p.Datastores, datastore) {
		return name
	}
	return ""
}

func (pl *placement) networksFor(cluster string) []store.InvNetwork {
	c, r := pl.cluster(cluster), pl.rule(cluster)
	if r == nil {
		return nil
	}
	var out []store.InvNetwork
	for _, n := range pl.networks {
		if n.VCenter == c.VCenter && slices.Contains(n.Clusters, cluster) && matchAny(r.NetworkPatterns, n.Name) {
			out = append(out, n)
		}
	}
	return out
}
