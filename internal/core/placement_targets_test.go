package core

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/store"
)

// pooled is three clusters with 10, 30 and 60 GB of free memory and 5, 1
// and 3 of rvav's VMs; c has three hosts, the last tagged for GPUs and the
// first in maintenance, and a datastore cluster of two datastores.
func pooled(strategy string) *placement {
	cl := func(name string, freeGB int64) store.InvCluster {
		return store.InvCluster{VCenter: "vc", Name: name, HostCount: 3, MemoryTotalMB: 200_000, MemoryUsedMB: 200_000 - freeGB*1024,
			Tags: []string{"rvav-env/prod"}}
	}
	ds := func(name, cluster string, free int64, tags ...string) store.InvDatastore {
		return store.InvDatastore{VCenter: "vc", Name: name, FreeGB: free, Clusters: []string{cluster}, Tags: tags}
	}
	host := func(name, state string, tags ...string) store.InvHost {
		return store.InvHost{VCenter: "vc", Name: name, Cluster: "c", State: state, Tags: tags}
	}
	clusters := []store.InvCluster{cl("a", 10), cl("b", 30), cl("c", 60)}
	pl := &placement{
		rules: []store.PlacementRule{{ClusterPatterns: []string{"*"}, DatastorePatterns: []string{"*"}, NetworkPatterns: []string{"*"},
			Strategy: strategy}},
		all: clusters, clusters: clusters,
		datastores: []store.InvDatastore{ds("a-1", "a", 1000), ds("b-1", "b", 1000), ds("c-1", "c", 3000),
			ds("c-pod-1", "c", 500, "rvav-tier/gold"), ds("c-pod-2", "c", 800)},
		hosts: []store.InvHost{host("c-h1", vsphere.HostMaintenance), host("c-h2", vsphere.HostConnected),
			host("c-h3", vsphere.HostConnected, "rvav-hw/gpu")},
		pools: []store.InvResourcePool{{VCenter: "vc", Cluster: "b", Path: "rvav"}, {VCenter: "vc", Cluster: "b", Path: "rvav/dev"},
			{VCenter: "vc", Cluster: "c", Path: "rvav"}},
		pods: []store.InvDatastoreCluster{{VCenter: "vc", Name: "c-pod", Datastores: []string{"c-pod-1", "c-pod-2"}, SDRS: true,
			Tags: []string{"rvav-pod/yes"}}},
		vms: map[string]int{"vc/a": 5, "vc/b": 1, "vc/c": 3},
	}
	inheritPodTags(pl.datastores, pl.pods)
	return pl
}

func chooseIn(t *testing.T, pl *placement, plan blueprint.VMPlan, count int, cons ...string) clusterChoice {
	t.Helper()
	plan.DiskGB = cmp.Or(plan.DiskGB, 50)
	ch, _, why := pl.choose(placementQuery{plan: plan, count: count, anyDatastore: true, hasTemplate: func(string) bool { return true },
		cons: constraints(t, cons...), vmGB: plan.DiskGB})
	if why != "" {
		t.Fatal(why)
	}
	return ch
}

func TestStrategies(t *testing.T) {
	for _, tc := range []struct {
		name, strategy string
		memoryGB       int
		cluster, note  string
	}{
		{name: "most free memory, the default", memoryGB: 8, cluster: "c"},
		{name: "binpack: the least free memory with room", strategy: store.StrategyBinpack, memoryGB: 8, cluster: "b",
			note: "binpack chose b: the least free memory with room for 2 VMs of 8 GB"},
		{name: "binpack: room on c only", strategy: store.StrategyBinpack, memoryGB: 16, cluster: "c",
			note: "binpack chose c: the least free memory with room for 2 VMs of 16 GB"},
		{name: "binpack: room nowhere", strategy: store.StrategyBinpack, memoryGB: 64, cluster: "c",
			note: "binpack chose c: no cluster has free memory for 2 VMs of 64 GB, it has the most"},
		{name: "spread: the fewest of rvav's VMs", strategy: store.StrategySpread, memoryGB: 8, cluster: "b",
			note: "spread chose b: the fewest of rvav's VMs (1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := chooseIn(t, pooled(tc.strategy), blueprint.VMPlan{MemoryGB: tc.memoryGB}, 2)
			if ch.cluster.Name != tc.cluster || ch.note != tc.note {
				t.Errorf("chose %s (%q), want %s (%q)", ch.cluster.Name, ch.note, tc.cluster, tc.note)
			}
			if want := cmp.Or(tc.strategy, store.StrategyMostFreeMemory); ch.strategy != want {
				t.Errorf("strategy %s, want %s", ch.strategy, want)
			}
		})
	}
	// The request's own VMs count: b, given 4 by an earlier component, has
	// more than c.
	pl := pooled(store.StrategySpread)
	pl.planned = map[string]int{"vc/b": 4}
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 2); ch.cluster.Name != "c" {
		t.Errorf("spread with the request's VMs chose %s, want c", ch.cluster.Name)
	}
	// Ties go to the most free memory.
	pl = pooled(store.StrategySpread)
	pl.vms = map[string]int{}
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 2); ch.cluster.Name != "c" {
		t.Errorf("spread tie chose %s, want c", ch.cluster.Name)
	}
	// The strategy is that of the first rule, by priority, placing a
	// candidate: the binpack rule for b and c comes before the spread one.
	pl = pooled(store.StrategySpread)
	pl.rules = append([]store.PlacementRule{{ClusterPatterns: []string{"b", "c"}, DatastorePatterns: []string{"*"}, NetworkPatterns: []string{"*"},
		Strategy: store.StrategyBinpack}}, pl.rules...)
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 2); ch.strategy != store.StrategyBinpack || ch.cluster.Name != "b" {
		t.Errorf("chose %s by %s, want b by binpack", ch.cluster.Name, ch.strategy)
	}
}

func TestStrategiesAfterConstraints(t *testing.T) {
	pl := pooled(store.StrategySpread)
	pl.clusters[1].Tags = []string{"rvav-env/test"}
	pl.all = pl.clusters
	// b has the fewest VMs, but isn't tagged prod: spread chooses among
	// those left.
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 1, "compute rvav-env/prod"); ch.cluster.Name != "c" {
		t.Errorf("chose %s, want c", ch.cluster.Name)
	}
}

func TestHostTagsAndPatterns(t *testing.T) {
	pl := pooled("")
	// A compute constraint a host's tag meets: only c, on its GPU host.
	ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 1, "compute rvav-hw/gpu")
	if ch.cluster.Name != "c" || !slices.Equal(ch.hosts, []string{"c-h3"}) {
		t.Errorf("chose %s on %v, want c on c-h3", ch.cluster.Name, ch.hosts)
	}
	// Without one: any host that takes new VMs, so none named.
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 1); ch.hosts != nil {
		t.Errorf("hosts %v, want nil", ch.hosts)
	}
	// Two VMs kept apart need two hosts: c-h1 is in maintenance, so GPU VMs
	// can't be kept apart anywhere.
	if ch, _, _ := pl.choose(placementQuery{plan: blueprint.VMPlan{MemoryGB: 8, DiskGB: 50}, count: 2, apart: true, anyDatastore: true,
		hasTemplate: func(string) bool { return true }, cons: constraints(t, "compute rvav-hw/gpu"), vmGB: 50}); ch.cluster != nil {
		t.Errorf("chose %s for VMs kept apart on one GPU host", ch.cluster.Name)
	}
	// A soft one keeps them apart on hosts without it.
	if ch, _, _ := pl.choose(placementQuery{plan: blueprint.VMPlan{MemoryGB: 8, DiskGB: 50}, count: 2, apart: true, anyDatastore: true,
		hasTemplate: func(string) bool { return true }, cons: constraints(t, "compute rvav-hw/gpu:soft", "compute rvav-env/prod"), vmGB: 50}); ch.cluster == nil ||
		ch.cluster.Name != "c" || ch.hosts != nil {
		t.Errorf("soft and apart: %+v", ch)
	}
	// A rule's host patterns narrow them.
	pl.rules[0].HostPatterns = []string{"*-h2"}
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 1); ch.cluster.Name != "c" || !slices.Equal(ch.hosts, []string{"c-h2"}) {
		t.Errorf("chose %s on %v, want c on c-h2", ch.cluster.Name, ch.hosts)
	}
	if _, _, why := pl.choose(placementQuery{plan: blueprint.VMPlan{MemoryGB: 8, DiskGB: 50}, count: 1, anyDatastore: true,
		hasTemplate: func(string) bool { return true }, cons: constraints(t, "compute rvav-hw/gpu"), vmGB: 50}); why !=
		"This item needs a cluster tagged rvav-hw/gpu, and none allowed here is." {
		t.Errorf("why %q", why)
	}
	// A host bound.
	pl.rules[0].HostPatterns = nil
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8, Host: "c-h2"}, 1); ch.cluster.Name != "c" || !slices.Equal(ch.hosts, []string{"c-h2"}) {
		t.Errorf("bound: chose %s on %v", ch.cluster.Name, ch.hosts)
	}
}

// TestHostsLeftOutStayOut: a host in maintenance at plan time that the
// rule's patterns, the host bound or a constraint leave out is left out
// by name, so it doesn't take the VMs once back (T1.7).
func TestHostsLeftOutStayOut(t *testing.T) {
	pl := pooled("")
	pl.hosts[0].Tags = []string{"rvav-zone/pci"} // c-h1, in maintenance
	want := []string{"c-h2", "c-h3"}
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 1, "compute !rvav-zone/pci"); !slices.Equal(ch.hosts, want) {
		t.Errorf("a constraint: %v", ch.hosts)
	}
	if hosts, _ := pl.placeOnGiven(pl.cluster("c"), "", constraints(t, "compute !rvav-zone/pci"), nil); !slices.Equal(hosts, want) {
		t.Errorf("a constraint on the cluster given: %v", hosts)
	}
	// A soft one let go, or one c-h1 meets too, rules out nothing.
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 1, "compute rvav-hw/fpga:soft", "compute rvav-env/prod"); ch.hosts != nil {
		t.Errorf("a soft constraint let go: %v", ch.hosts)
	}
	pl.rules[0].HostPatterns = []string{"c-h[23]"}
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8}, 1); !slices.Equal(ch.hosts, want) {
		t.Errorf("host patterns: %v", ch.hosts)
	}
	pl.rules[0].HostPatterns = nil
	pl.hosts = pl.hosts[:2]
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8, Host: "c-h2"}, 1); !slices.Equal(ch.hosts, []string{"c-h2"}) {
		t.Errorf("a host bound: %v", ch.hosts)
	}
}

func TestPlaceOnGiven(t *testing.T) {
	pl := pooled("")
	var said []string
	add := func(code, msg string) { said = append(said, code+": "+msg) }
	hosts, results := pl.placeOnGiven(pl.cluster("c"), "", constraints(t, "compute rvav-hw/gpu"), add)
	if !slices.Equal(hosts, []string{"c-h3"}) || len(said) > 0 || len(results) != 1 || !results[0].met {
		t.Errorf("gpu on c: %v %v %s", hosts, said, said)
	}
	if hosts, _ := pl.placeOnGiven(pl.cluster("c"), "", constraints(t, "compute !rvav-hw/gpu"), add); !slices.Equal(hosts, []string{"c-h2"}) {
		t.Errorf("not gpu on c: %v", hosts)
	}
	pl.placeOnGiven(pl.cluster("c"), "c-h1", nil, add)
	pl.placeOnGiven(pl.cluster("c"), "c-h2", constraints(t, "compute rvav-hw/gpu"), add)
	// a has no host in the inventory: its tags, as before.
	pl.placeOnGiven(pl.cluster("a"), "", constraints(t, "compute rvav-hw/gpu"), add)
	want := []string{
		"not_allowed: Host c-h1 is not one of c's hosts its rule allows, or it takes no new VMs.",
		"constraint: No host of c that takes new VMs is tagged rvav-hw/gpu, which this item needs.",
		"constraint: Cluster a isn't tagged rvav-hw/gpu, which this item needs.",
	}
	if !slices.Equal(said, want) {
		t.Errorf("said\n%s\nwant\n%s", strings.Join(said, "\n"), strings.Join(want, "\n"))
	}
}

func TestResourcePoolAndDatastoreCluster(t *testing.T) {
	pl := pooled("")
	// A pool rules out the clusters without it.
	if ch := chooseIn(t, pl, blueprint.VMPlan{MemoryGB: 8, ResourcePool: "rvav/dev"}, 1); ch.cluster.Name != "b" {
		t.Errorf("pool rvav/dev: chose %s, want b", ch.cluster.Name)
	}
	r := store.PlacementRule{ClusterPatterns: []string{"*"}, ResourcePool: "rvav"}
	if pl.allows(r, *pl.cluster("a")) || !pl.allows(r, *pl.cluster("c")) {
		t.Error("a rule's pool: a has none, c has one")
	}
	// The members of a datastore cluster carry its tags.
	if got := pl.datastoresFor("c")[1].Tags; !slices.Equal(got, []string{"rvav-pod/yes", "rvav-tier/gold"}) {
		t.Errorf("c-pod-1 tags %v", got)
	}
	// A blueprint's datastore cluster: the roomiest member.
	if ds, _, _ := pl.pickDatastore("c", "c-pod", 100, nil); ds != "c-pod-2" {
		t.Errorf("pod: %s, want c-pod-2", ds)
	}
	if ds, _, _ := pl.pickDatastore("c", "c-pod", 100, constraints(t, "storage rvav-tier/gold")); ds != "c-pod-1" {
		t.Errorf("pod and gold: %s, want c-pod-1", ds)
	}
	if pl.podOf("c", "c-pod", "c-pod-2") != "c-pod" || pl.podOf("c", "c-pod", "c-1") != "" || pl.podOf("c", "", "c-pod-2") != "" {
		t.Error("podOf")
	}
	// A rule's datastore cluster: its members only, narrowed by its patterns.
	pl.rules[0].DatastoreCluster, pl.rules[0].DatastorePatterns = "c-pod", nil
	names := func(list []store.InvDatastore) []string {
		var out []string
		for _, d := range list {
			out = append(out, d.Name)
		}
		return out
	}
	if got := names(pl.datastoresFor("c")); !slices.Equal(got, []string{"c-pod-1", "c-pod-2"}) {
		t.Errorf("rule's pod: %v", got)
	}
	if pl.podOf("c", "", "c-pod-1") != "c-pod" {
		t.Error("podOf by the rule")
	}
	pl.rules[0].DatastorePatterns = []string{"*-2"}
	if got := names(pl.datastoresFor("c")); !slices.Equal(got, []string{"c-pod-2"}) {
		t.Errorf("rule's pod and pattern: %v", got)
	}
	// Room in the pod only: 900 GB fits nowhere in it.
	if ch, _, _ := pl.choose(placementQuery{plan: blueprint.VMPlan{MemoryGB: 8, DiskGB: 900}, count: 1, anyDatastore: true,
		hasTemplate: func(string) bool { return true }, vmGB: 900}); ch.cluster != nil && ch.cluster.Name == "c" {
		t.Error("chose c without room in its datastore cluster")
	}
}

func TestPlacementNotesWithStrategies(t *testing.T) {
	met := constraintResult{Constraint: constraints(t, "compute rvav-env/prod")[0], object: "b", met: true}
	ev := &Evaluation{Parts: []vmPart{
		{label: "Web: ", strategyNote: "binpack chose b: the least free memory with room for 2 VMs of 8 GB", constraints: []constraintResult{met}},
		{label: "Db: "},
	}}
	if got, want := ev.placementNotes(), []string{"Web: binpack chose b: the least free memory with room for 2 VMs of 8 GB; placed by rvav-env/prod"}; !slices.Equal(got, want) {
		t.Errorf("notes %q, want %q", got, want)
	}
	if got := ev.placementTitle(); got != "Placed by rules and constraint tags" {
		t.Errorf("title %q", got)
	}
	ev.Parts[0].constraints = nil
	if got := ev.placementTitle(); got != "Placed by rules" {
		t.Errorf("title %q", got)
	}
	single := &Evaluation{Parts: []vmPart{{strategyNote: "spread chose b: the fewest of rvav's VMs (1)"}}}
	if got := single.placementNotes(); !slices.Equal(got, []string{"Spread chose b: the fewest of rvav's VMs (1)"}) {
		t.Errorf("single %q", got)
	}
	if got := (&Evaluation{Parts: []vmPart{{constraints: []constraintResult{met}}}}).placementTitle(); got != "" {
		t.Errorf("constraints only: %q", got)
	}
}
