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
}

type Service struct {
	Store     *store.Store
	IPAM      efficientip.Client
	Workflows Workflows
	Events    *events.Publisher
	Log       *slog.Logger
	// VCenter gives WebMKS tickets for consoles (ADR-0012); it may connect
	// on first use.
	VCenter func(ctx context.Context) (ConsoleSource, error)
	// ConsoleAccount, when set, is a session of a vCenter account that may
	// only open consoles: VMRC links carry a clone ticket of it. Never
	// rvav's own session, which a clone ticket would hand to the user.
	ConsoleAccount func(ctx context.Context) (CloneTicketer, error)
	// AWXCatalog imports AWX templates into the catalog (ADR-0016).
	AWXCatalog *awxcatalog.Sync
	// AWX and its organisation give the awx.inventories and
	// awx.credentials pickers.
	AWX             awx.Client
	AWXOrganization string
	// Secrets seals the secrets value sources use; REST calls the URLs of
	// `rest` value sources (ADR-0019).
	Secrets *secrets.Box
	REST    *RESTSources
	// Config is the configuration this process started with, saved
	// settings applied; SettingsRevision is theirs (ADR-0020).
	Config           config.Config
	SettingsRevision int64
	// Dispatcher delivers events to subscriptions: their test runs here
	// (ADR-0021).
	Dispatcher *dispatch.Dispatcher
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
	if !p.CanSeeProject(proj.ID) {
		return proj, ErrNotFound
	}
	return proj, nil
}

// placement is what a project may use in one environment.
type placement struct {
	rules      []store.PlacementRule
	clusters   []store.InvCluster
	datastores []store.InvDatastore
	networks   []store.InvNetwork
	subnets    map[string]store.NetworkSubnet
	tags       map[string][]string // of every cluster, by name
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

func (s *Service) placement(ctx context.Context, projectID, environment string) (*placement, error) {
	rules, err := s.Store.PlacementRules(ctx, projectID, environment)
	if err != nil {
		return nil, err
	}
	pl := &placement{rules: rules}
	if len(rules) == 0 {
		return pl, nil
	}
	clusters, err := s.Store.InventoryClusters(ctx)
	if err != nil {
		return nil, err
	}
	pl.tags = map[string][]string{}
	for _, c := range clusters {
		pl.tags[c.Name] = c.Tags
	}
	for _, c := range clusters {
		if pl.rule(c.Name) != nil {
			pl.clusters = append(pl.clusters, c)
		}
	}
	if pl.datastores, err = s.Store.InventoryDatastores(ctx); err != nil {
		return nil, err
	}
	if pl.networks, err = s.Store.InventoryNetworks(ctx); err != nil {
		return nil, err
	}
	if pl.subnets, err = s.Store.NetworkSubnets(ctx); err != nil {
		return nil, err
	}
	return pl, nil
}

// rule returns the first rule allowing the cluster.
func (pl *placement) rule(cluster string) *store.PlacementRule {
	for i := range pl.rules {
		if selects(pl.rules[i].ClusterPatterns, pl.rules[i].ClusterTags, cluster, pl.tags[cluster]) {
			return &pl.rules[i]
		}
	}
	return nil
}

func (pl *placement) cluster(name string) *store.InvCluster {
	for i := range pl.clusters {
		if pl.clusters[i].Name == name {
			return &pl.clusters[i]
		}
	}
	return nil
}

func (pl *placement) datastoresFor(cluster string) []store.InvDatastore {
	r := pl.rule(cluster)
	if r == nil {
		return nil
	}
	var out []store.InvDatastore
	for _, d := range pl.datastores {
		if slices.Contains(d.Clusters, cluster) && selects(r.DatastorePatterns, r.DatastoreTags, d.Name, d.Tags) {
			out = append(out, d)
		}
	}
	return out
}

func (pl *placement) networksFor(cluster string) []store.InvNetwork {
	r := pl.rule(cluster)
	if r == nil {
		return nil
	}
	var out []store.InvNetwork
	for _, n := range pl.networks {
		if slices.Contains(n.Clusters, cluster) && matchAny(r.NetworkPatterns, n.Name) {
			out = append(out, n)
		}
	}
	return out
}
