package core

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/store"
)

// tagged is two vCenter clusters, prod and dev, each with a gold and a
// bronze datastore and a network; app-2 is tagged DMZ, with the most free
// memory.
func tagged() *placement {
	cl := func(name string, freeMB int64, tags ...string) store.InvCluster {
		return store.InvCluster{VCenter: "vc", Name: name, HostCount: 4, MemoryTotalMB: 100_000, MemoryUsedMB: 100_000 - freeMB, Tags: tags}
	}
	ds := func(name, cluster string, free int64, tags ...string) store.InvDatastore {
		return store.InvDatastore{VCenter: "vc", Name: name, FreeGB: free, Clusters: []string{cluster}, Tags: tags}
	}
	nw := func(name, cluster string, tags ...string) store.InvNetwork {
		return store.InvNetwork{VCenter: "vc", Name: name, Clusters: []string{cluster}, Tags: tags}
	}
	clusters := []store.InvCluster{
		cl("app-1", 10_000, "rvav-env/prod"),
		cl("app-2", 50_000, "rvav-env/prod", "rvav-zone/dmz"),
		cl("dev-1", 90_000, "rvav-env/dev"),
	}
	return &placement{
		rules:    []store.PlacementRule{{ClusterPatterns: []string{"*"}, DatastorePatterns: []string{"*"}, NetworkPatterns: []string{"*"}}},
		all:      clusters,
		clusters: clusters,
		datastores: []store.InvDatastore{
			ds("app-1-gold", "app-1", 500, "rvav-tier/gold"), ds("app-1-bronze", "app-1", 900, "rvav-tier/bronze"),
			ds("app-2-gold", "app-2", 800, "rvav-tier/gold"), ds("app-2-bronze", "app-2", 2000, "rvav-tier/bronze"),
			ds("dev-1-bronze", "dev-1", 4000, "rvav-tier/bronze"),
		},
		networks: []store.InvNetwork{
			nw("app-1-web", "app-1"), nw("app-1-pci", "app-1", "pci/yes"),
			nw("app-2-web", "app-2"), nw("app-2-pci", "app-2", "pci/yes"),
			nw("dev-1-web", "dev-1"),
		},
		subnets: map[string]store.NetworkSubnet{"app-1-web": {}, "app-1-pci": {}, "app-2-web": {}, "app-2-pci": {}, "dev-1-web": {}},
	}
}

func constraints(t *testing.T, list ...string) constraintSet {
	t.Helper()
	var out constraintSet
	for _, s := range list {
		kind, text, _ := strings.Cut(s, " ")
		c, err := blueprint.ParseConstraint(text)
		if err != nil {
			t.Fatal(err)
		}
		c.Kind = kind
		out = append(out, c)
	}
	return out
}

// said is what constraint results say, one line each.
func said(results []constraintResult) string {
	var out []string
	for _, r := range results {
		out = append(out, fmt.Sprintf("%s %s on %q met=%v excluded=%v", r.Kind, r, r.object, r.met, r.excluded))
	}
	return strings.Join(out, "\n")
}

func TestChooseByConstraints(t *testing.T) {
	anywhere := func(string) bool { return true }
	for _, tc := range []struct {
		name    string
		cons    []string
		network bool
		cluster string
		said    string
		why     string
	}{
		{name: "none: the most free memory", cluster: "dev-1"},
		{name: "hard", cons: []string{"compute rvav-env/prod"}, cluster: "app-2",
			said: `compute rvav-env/prod on "app-2" met=true excluded=[dev-1]`},
		{name: "negation", cons: []string{"compute rvav-env/prod", "compute !rvav-zone/dmz"}, cluster: "app-1",
			said: "compute rvav-env/prod on \"app-1\" met=true excluded=[dev-1]\ncompute !rvav-zone/dmz on \"app-1\" met=true excluded=[app-2]"},
		{name: "soft met", cons: []string{"compute rvav-env/prod:soft"}, cluster: "app-2",
			said: `compute rvav-env/prod:soft on "app-2" met=true excluded=[dev-1]`},
		{name: "soft let go", cons: []string{"compute rvav-env/test:soft"}, cluster: "dev-1",
			said: `compute rvav-env/test:soft on "dev-1" met=false excluded=[]`},
		{name: "hard storage rules out a cluster", cons: []string{"storage rvav-tier/gold"}, cluster: "app-2"},
		{name: "hard network rules out a cluster", cons: []string{"compute !rvav-zone/dmz", "network pci/yes"}, network: true, cluster: "app-1",
			said: `compute !rvav-zone/dmz on "app-1" met=true excluded=[app-2]`},
		{name: "hard nothing meets", cons: []string{"compute rvav-env/prod", "compute rvav-env/test"},
			why: "This item needs a cluster tagged rvav-env/test, and none allowed here is."},
		{name: "negation nothing meets", cons: []string{"compute rvav-env/prod", "compute !rvav-env/prod"},
			why: "This item needs a cluster not tagged rvav-env/prod, and none allowed here is."},
		{name: "storage nothing meets", cons: []string{"storage rvav-tier/platinum"},
			why: "This item needs a datastore tagged rvav-tier/platinum, and no cluster allowed here has one."},
		{name: "network nothing meets", cons: []string{"compute rvav-env/dev", "network pci/yes"}, network: true,
			why: "This item needs a network tagged pci/yes with a subnet, and no cluster allowed here has one."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl, results, why := tagged().choose(placementQuery{plan: blueprint.VMPlan{DiskGB: 100}, count: 2, anyDatastore: true,
				anyNetwork: tc.network, hasTemplate: anywhere, cons: constraints(t, tc.cons...), vmGB: 100})
			got := ""
			if cl != nil {
				got = cl.Name
			}
			if got != tc.cluster || why != tc.why {
				t.Fatalf("chose %q (%s), want %q (%s)", got, why, tc.cluster, tc.why)
			}
			if tc.said != "" && said(results) != tc.said {
				t.Errorf("said\n%s\nwant\n%s", said(results), tc.said)
			}
		})
	}
}

func TestChooseKeepsRoomAndHosts(t *testing.T) {
	pl := tagged()
	// Gold on app-2 has 800 GB: three VMs of 300 GB don't fit, those of app-1 neither.
	cl, _, why := pl.choose(placementQuery{plan: blueprint.VMPlan{DiskGB: 300}, count: 3, anyDatastore: true,
		hasTemplate: func(string) bool { return true }, cons: constraints(t, "storage rvav-tier/gold"), vmGB: 300})
	if cl != nil || why != "" {
		t.Errorf("chose %v (%s), want no room", cl, why)
	}
	// A disk of its own on gold, the rest on bronze.
	cons := constraints(t, "storage rvav-tier/bronze")
	cons = append(cons, constraints(t, "storage rvav-tier/gold")...)
	cons[1].Disk = 1
	cl, _, _ = pl.choose(placementQuery{plan: blueprint.VMPlan{DiskGB: 50, Disks: []blueprint.DiskPlan{{SizeGB: 300}}}, count: 2,
		anyDatastore: true, hasTemplate: func(string) bool { return true }, cons: cons, disks: map[int]int{1: 300}, vmGB: 50})
	if cl == nil || cl.Name != "app-2" {
		t.Errorf("chose %v, want app-2", cl)
	}
}

func TestPickDatastoreAndNetwork(t *testing.T) {
	pl := tagged()
	ds, results, why := pl.pickDatastore("app-2", 100, constraints(t, "storage rvav-tier/gold:soft"))
	if ds != "app-2-gold" || why != "" || said(results) != `storage rvav-tier/gold:soft on "app-2-gold" met=true excluded=[app-2-bronze]` {
		t.Errorf("soft met: %s %s %s", ds, why, said(results))
	}
	ds, results, _ = pl.pickDatastore("app-2", 1000, constraints(t, "storage rvav-tier/gold:soft"))
	if ds != "app-2-bronze" || said(results) != `storage rvav-tier/gold:soft on "app-2-bronze" met=false excluded=[]` {
		t.Errorf("soft let go: %s %s", ds, said(results))
	}
	if ds, _, why = pl.pickDatastore("app-2", 1000, constraints(t, "storage rvav-tier/gold")); ds != "" ||
		why != "This item needs a datastore tagged rvav-tier/gold on app-2 with room for 1000 GB, and none allowed is." {
		t.Errorf("hard: %s %s", ds, why)
	}
	if ds, _, _ = pl.pickDatastore("app-2", 100, constraints(t, "storage !rvav-tier/gold")); ds != "app-2-bronze" {
		t.Errorf("negation: %s", ds)
	}
	n, results, _ := pl.pickNetwork("app-1", constraints(t, "network pci/yes"))
	if n != "app-1-pci" || said(results) != `network pci/yes on "app-1-pci" met=true excluded=[app-1-web]` {
		t.Errorf("network: %s %s", n, said(results))
	}
	if n, _, _ = pl.pickNetwork("app-1", constraints(t, "network !pci/yes")); n != "app-1-web" {
		t.Errorf("network negation: %s", n)
	}
	if n, _, why = pl.pickNetwork("dev-1", constraints(t, "network pci/yes")); n != "" ||
		why != "This item needs a network tagged pci/yes on dev-1 with a subnet, and none allowed is." {
		t.Errorf("network hard: %s %s", n, why)
	}
}

func TestCheckGivenAndNote(t *testing.T) {
	var errs []string
	add := func(code, msg string) { errs = append(errs, code+": "+msg) }
	cons := constraints(t, "compute rvav-env/prod", "compute !rvav-zone/dmz", "compute rvav-site/gva:soft")
	results := checkGiven(cons, "Cluster", "app-2", []string{"rvav-env/prod", "rvav-zone/dmz"}, add)
	if want := []string{"constraint: Cluster app-2 is tagged rvav-zone/dmz, which this item rules out."}; !slices.Equal(errs, want) {
		t.Errorf("errors %q, want %q", errs, want)
	}
	disk := constraints(t, "storage rvav-tier/gold")
	disk[0].Disk = 2
	checkGiven(disk, "Datastore", "app-2-bronze", []string{"rvav-tier/bronze"}, add)
	if want := "constraint: Datastore app-2-bronze isn't tagged rvav-tier/gold, which disk 3 needs."; errs[1] != want {
		t.Errorf("disk error %q, want %q", errs[1], want)
	}
	if got, want := placementNote("Web: ", results),
		"Web: placed by rvav-env/prod; soft constraint rvav-site/gva not met: placed on app-2 without it"; got != want {
		t.Errorf("note %q, want %q", got, want)
	}
	if got := placementNote("", nil); got != "" {
		t.Errorf("no constraints, note %q", got)
	}
	if got, want := placementNote("", results[:1]), "Placed by rvav-env/prod"; got != want {
		t.Errorf("single VM note %q, want %q", got, want)
	}
}
