package provision

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/damhau/rvav/internal/blueprint"
)

// A component is built in the round after those it depends on, with what
// they built; the others at the same time (T1.1).
func TestComponentsBuiltInRounds(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	info := Info{RequestID: "req_1", Number: "REQ-1", ProjectID: "prj_web", Status: "queued",
		Plan: Plan{Kind: blueprint.KindVM,
			VMs: []VMUnit{
				{DeploymentID: "dep_1", Component: "web", Name: "web01", FQDN: "web01.corp"},
				{DeploymentID: "dep_1", Component: "db", Name: "db01", FQDN: "db01.corp"},
				{DeploymentID: "dep_1", Component: "db", Index: 1, Name: "db02", FQDN: "db02.corp"},
				{DeploymentID: "dep_1", Component: "cache", Name: "cache01", FQDN: "cache01.corp"}},
			Components: []ComponentUnit{{Key: "web", DependsOn: []string{"db"}, Reads: []string{"db", "web"}}, {Key: "db"}, {Key: "cache"}}}}
	ips := map[string]string{"web01": "10.0.0.30", "db01": "10.0.0.10", "db02": "10.0.0.11", "cache01": "10.0.0.20"}
	var mu sync.Mutex
	var order []string
	seen := map[string]VMInput{}
	record := func(what string, in VMInput) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, what+" "+in.VM.Name)
		seen[what+" "+in.VM.Name] = in
	}
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(info, nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.MarkStep, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.ReserveIP, mock.Anything, mock.Anything).Return(func(_ context.Context, in VMInput) (string, error) {
		record("ip", in)
		return ips[in.VM.Name], nil
	})
	env.OnActivity(a.CreateDNS, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.CloneVM, mock.Anything, mock.Anything).Return(func(_ context.Context, in VMInput) (string, error) {
		record("clone", in)
		return "vm-" + in.VM.Name, nil
	})
	env.OnActivity(a.Customize, mock.Anything, mock.Anything).Return(func(_ context.Context, in VMInput) error {
		record("customize", in)
		return nil
	})
	env.OnActivity(a.PowerOn, mock.Anything, mock.Anything).Return(func(_ context.Context, in VMInput) (string, error) {
		record("power", in)
		return in.IP, nil
	})
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_1", ProjectID: "prj_web", Outcome: "succeeded"}).Return(nil)

	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	at := func(what string) int { return slices.Index(order, what) }
	for _, before := range []string{"power db01", "power db02", "power cache01"} {
		if at(before) < 0 || at(before) > at("ip web01") {
			t.Fatalf("web was built before %s: %v", before, order)
		}
	}
	// Its clone has db's addresses; its customization its own too.
	if in := seen["ip db01"]; in.Components != nil {
		t.Errorf("db reads nothing, was given %v", in.Components)
	}
	got, _ := json.Marshal(seen["clone web01"].Components)
	want := `{"db":{"addresses":["10.0.0.10","10.0.0.11"],"count":2,"fqdns":["db01.corp","db02.corp"],"names":["db01","db02"],` +
		`"vms":[{"address":"10.0.0.10","fqdn":"db01.corp","index":0,"name":"db01"},{"address":"10.0.0.11","fqdn":"db02.corp","index":1,"name":"db02"}]},` +
		`"web":{"addresses":[],"count":0,"fqdns":[],"names":[],"vms":[]}}`
	if string(got) != want {
		t.Errorf("web's clone read\n%s\nwant\n%s", got, want)
	}
	if web := seen["customize web01"].Components["web"].(map[string]any); web["count"] != float64(1) && web["count"] != 1 {
		t.Errorf("web's customization read %v of itself", web)
	}
}

// A component on DHCP is read with the address its VMs report once on
// (ADR-0039).
func TestComponentsReadDHCPAddresses(t *testing.T) {
	env := newEnv(t)
	var a *Activities
	info := Info{RequestID: "req_1", Number: "REQ-1", ProjectID: "prj_web", Status: "queued",
		Plan: Plan{Kind: blueprint.KindVM,
			VMs: []VMUnit{
				{DeploymentID: "dep_1", Component: "web", Name: "web01", FQDN: "web01.corp"},
				{DeploymentID: "dep_1", Component: "db", Name: "db01", FQDN: "db01.corp", DHCP: true}},
			Components: []ComponentUnit{{Key: "web", DependsOn: []string{"db"}, Reads: []string{"db"}}, {Key: "db"}}}}
	var web VMInput
	env.OnActivity(a.LoadPlan, mock.Anything, "req_1").Return(info, nil)
	env.OnActivity(a.MarkRunning, mock.Anything, "req_1").Return(true, nil)
	env.OnActivity(a.MarkStep, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.ReserveIP, mock.Anything, mock.MatchedBy(func(in VMInput) bool { return in.VM.Name == "web01" })).Return("10.0.0.30", nil).Once()
	env.OnActivity(a.CreateDNS, mock.Anything, mock.MatchedBy(func(in VMInput) bool { return in.VM.Name == "web01" })).Return(nil).Once()
	env.OnActivity(a.CloneVM, mock.Anything, mock.Anything).Return(func(_ context.Context, in VMInput) (string, error) {
		if in.VM.Name == "web01" {
			web = in
		}
		return "vm-" + in.VM.Name, nil
	})
	env.OnActivity(a.Customize, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.PowerOn, mock.Anything, mock.Anything).Return(func(_ context.Context, in VMInput) (string, error) {
		if in.VM.DHCP {
			return "10.0.0.99", nil
		}
		return in.IP, nil
	})
	env.OnActivity(a.Finish, mock.Anything, FinishInput{RequestID: "req_1", ProjectID: "prj_web", Outcome: "succeeded"}).Return(nil)

	env.ExecuteWorkflow(Provision, "req_1")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(web.Components["db"].(map[string]any)["addresses"]); string(got) != `["10.0.0.99"]` {
		t.Errorf("web read db's addresses %s", got)
	}
}

// Without dependencies, every component is in one round.
func TestBuildRounds(t *testing.T) {
	keys := func(rounds [][]ComponentUnit) [][]string {
		var out [][]string
		for _, r := range rounds {
			var ks []string
			for _, c := range r {
				ks = append(ks, c.Key)
			}
			out = append(out, ks)
		}
		return out
	}
	flat := buildRounds([]ComponentUnit{{Key: "a"}, {Key: "b", DependsOn: []string{"gone"}}})
	if got := keys(flat); len(got) != 1 || !slices.Equal(got[0], []string{"a", "b"}) {
		t.Errorf("no dependency: %v", got)
	}
	chain := buildRounds([]ComponentUnit{{Key: "web", DependsOn: []string{"app"}}, {Key: "app", DependsOn: []string{"db"}}, {Key: "db"}, {Key: "dns"}})
	if got, _ := json.Marshal(keys(chain)); string(got) != `[["db","dns"],["app"],["web"]]` {
		t.Errorf("a chain: %s", got)
	}
}

func TestBuildTags(t *testing.T) {
	in := VMInput{IP: "10.0.0.30", VM: VMUnit{Name: "web02", Index: 1, Component: "web",
		TagsBinding: "['node/' + string(self.index), 'db/' + components.db.addresses[0], 'app/' + inputs.app.lowerAscii()]",
		TagsInputs:  map[string]any{"app": "Shop"}},
		Components: map[string]any{"db": map[string]any{"addresses": []any{"10.0.0.10"}, "count": float64(1)}}}
	tags, err := buildTags(in)
	if err != nil || !slices.Equal(tags, []string{"node/1", "db/10.0.0.10", "app/shop"}) {
		t.Fatalf("tags %v, %v", tags, err)
	}
	in.VM.TagsBinding = "components.db.count == 1 ? 'one/db' : 'many/db'"
	if tags, err := buildTags(in); err != nil || !slices.Equal(tags, []string{"one/db"}) {
		t.Errorf("a count read back from history: %v, %v", tags, err)
	}
	in.VM.TagsBinding = "'no-category'"
	if _, err := buildTags(in); err == nil {
		t.Error("a tag without its category")
	}
}
