package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"
	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/provision"
)

// monitorStep is the step the composite designer adds (D1.3), as it writes
// it into the YAML: key, title, what it runs, its condition, then its
// inputs, each bound through the picker: the agent to the nodes' first
// FQDN (an earlier step's output), the port to a value written out.
const monitorStep = `
key: monitor
title: Monitoring agent
awx:
  job_template: monitoring-agent
when: inputs.data_gb >= 50
with:
  monitoring_agent: steps.nodes.fqdn
  listen_port: 9187
`

// TestD1Acceptance is the canvas milestone's acceptance (D1.5, ADR-0032),
// through the API the pages use. The seeded PostgreSQL composite is edited
// as the designer edits it (a step added after the nodes, its inputs bound
// to the nodes' outputs and to a value, a condition set), saved and
// published. Its request in production is approved by the Infrastructure
// leads, and followed on its event stream, as its page follows it, on the
// graph of the version requested: every step of the run is a node of that
// graph (submitted and the approval the Request node's, step:<key> each
// step's, ready the Done node's), the steps run in the graph's order, and
// the added step runs between the nodes and the database's owner with what
// it was bound to. Requested in development below its condition, the step
// is skipped. The seeded three-tier item's topology is on its form, and
// the item publishes and runs against the fake NSX Manager the compose
// stack runs (TestCompositeDesigner and TestRequestTopology hold the
// designer's targets and the topology's projection; the pages' specs are
// web/e2e/one-canvas.spec.ts, with the keyboard only, and
// composite-designer.spec.ts and request-run.spec.ts).
func TestD1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, dba, lead = "mkeller", "sbrunner", "mkeller"

		// What the designer's picker offers: AWX templates imported from AWX.
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log, Filter: awx.TemplateFilter{Labels: []string{"rvav"}}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", admin, nil, 200, nil)
		var targets api.CompositeStepTargetList
		h.json("GET", "/api/v1/admin/catalog/composite-steps", admin, nil, 200, &targets)
		var agent, nodes *api.CompositeStepTarget
		for i, tg := range targets.Items {
			switch {
			case tg.Kind == api.StepTargetAwx && tg.Ref == "monitoring-agent":
				agent = &targets.Items[i]
			case tg.Kind == api.StepTargetItem && tg.Ref == "pg-node":
				nodes = &targets.Items[i]
			}
		}
		if agent == nil || nodes == nil || !slices.ContainsFunc(nodes.Outputs, func(o api.CompositeStepOutput) bool { return o.Name == "fqdn" }) {
			t.Fatalf("the designer's targets: monitoring-agent %+v, pg-node %+v", agent, nodes)
		}

		// Edited on the canvas: a step after the nodes, each of its inputs
		// bound, a condition; saved and published.
		var step map[string]any
		if err := yaml.Unmarshal([]byte(monitorStep), &step); err != nil {
			t.Fatal(err)
		}
		for _, in := range agent.Inputs {
			if _, ok := step["with"].(map[string]any)[in.Name]; !ok {
				t.Fatalf("the step leaves %s of %v unbound", in.Name, agent.Inputs)
			}
		}
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/postgres-cluster/versions", admin, api.CatalogItemVersionCreate{}, 201, &draft)
		def := draft.Definition
		steps := def.Blueprint["steps"].([]any)
		def.Blueprint["steps"] = append([]any{steps[0], step}, steps[1:]...)
		h.json("PUT", "/api/v1/admin/catalog/items/postgres-cluster/versions/"+draft.Id, admin,
			api.CatalogItemVersionUpdate{Definition: def, BaseRevision: &draft.Revision}, 200, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/postgres-cluster/versions/"+draft.Id+"/publish", admin, nil, 200, nil)

		// The form draws the steps the run will: the step added, with its condition.
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/postgres-cluster/form?project_id=prj_data", dba, nil, 200, &form)
		if form.VersionId != draft.Id || form.Topology == nil {
			t.Fatalf("the form is of %s, not the version published (%s), or has no topology", form.VersionId, draft.Id)
		}
		graph := compositeNodes(t, *form.Topology)
		added := (*form.Topology)["steps"].([]any)[1].(map[string]any)
		if !slices.Equal(graph, []string{"inputs", "step:nodes", "step:monitor", "step:owner", "end"}) ||
			added["when"] != "inputs.data_gb >= 50" {
			t.Fatalf("the form's graph = %v, its step added %v", graph, added)
		}

		// Requested in production: the Infrastructure leads approve it.
		inputs := map[string]any{"name": "ledger", "pg_version": "16.6", "size": "S", "data_gb": 100,
			"cluster": "GVA-PRD-CL01", "datastore": "vsan-gva-prd-cl01", "network": "dvpg-db-prd-vl230"}
		var req api.Request
		h.json("POST", "/api/v1/requests", dba, api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId,
			ProjectId: "prj_data", Environment: "prd", Inputs: inputs}, 202, &req, "Idempotency-Key", uuid.NewString())
		if req.Status != api.RequestStatusPendingApproval {
			t.Fatalf("the production request is %s", req.Status)
		}
		// Its page: the graph of the version requested, read once.
		var top api.RequestTopology
		h.json("GET", "/api/v1/requests/"+req.Number+"/topology", dba, nil, 200, &top)
		if top.Topology == nil || !slices.Equal(compositeNodes(t, *top.Topology), graph) {
			t.Fatalf("the request's topology = %v", top.Topology)
		}
		// Then the statuses, live: the stream it follows.
		page := follow(t, h, req.Id, dba)
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				var inbox api.ApprovalPage
				h.json("GET", "/api/v1/approvals", lead, nil, 200, &inbox)
				if inbox.Total != 1 || inbox.Items[0].Request.Id != req.Id ||
					!slices.Contains(inbox.Items[0].Reasons, "Production database clusters always need approval") {
					t.Errorf("the leads' inbox = %+v", inbox)
					return
				}
				h.json("POST", "/api/v1/approvals/"+inbox.Items[0].Id+"/decision", lead,
					api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove}, 200, nil)
				h.wf.deliver(env)
			}, time.Hour)
		})
		h.json("GET", "/api/v1/requests/"+req.Id, dba, nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("the approved request = %s %s", req.Status, errorOf(req))
		}
		run := runOnGraph(t, h, req.Id, dba, graph, page)
		if run["inputs"][1].Title != "Approved by Marc Keller" {
			t.Fatalf("the Request node's steps = %+v", run["inputs"])
		}
		for _, id := range graph[1:4] {
			if s := run[id]; len(s) != 1 || s[0].Status != api.StepStatusDone {
				t.Fatalf("%s's steps = %+v", id, s)
			}
		}
		if s := run["end"]; len(s) != 1 || s[0].Key != "ready" || s[0].Status != api.StepStatusDone {
			t.Fatalf("the Done node's steps = %+v", s)
		}
		// The step added ran with what it was bound to: the first node's FQDN, the port.
		monitor := run["step:monitor"][0]
		if monitor.ExternalRef == nil {
			t.Fatalf("step:monitor ran no job: %+v", monitor)
		}
		job, _ := strconv.Atoi(monitor.ExternalRef.Id)
		vars := h.awx.Launched(job).ExtraVars
		if vars["monitoring_agent"] != "data-prd-pgledger01.corp.example" || vars["listen_port"] != float64(9187) {
			t.Fatalf("the monitoring job got %v", vars)
		}

		// In development, below its condition: the step is skipped, the rest runs.
		inputs = map[string]any{"name": "audit", "pg_version": "16.6", "size": "S", "data_gb": 20,
			"cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-db-dev-vl320"}
		var dev api.Request
		h.json("POST", "/api/v1/requests", dba, api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId,
			ProjectId: "prj_data", Environment: "dev", Inputs: inputs}, 202, &dev, "Idempotency-Key", uuid.NewString())
		page = follow(t, h, dev.Id, dba)
		h.runWorkflow(dev.Id, nil)
		h.json("GET", "/api/v1/requests/"+dev.Id, dba, nil, 200, &dev)
		if dev.Status != api.RequestStatusSucceeded {
			t.Fatalf("the development request = %s %s", dev.Status, errorOf(dev))
		}
		run = runOnGraph(t, h, dev.Id, dba, graph, page)
		if s := run["step:monitor"][0]; s.Status != api.StepStatusSkipped || deref(s.Detail) != "Its condition is false." {
			t.Fatalf("below its condition, step:monitor = %+v", s)
		}
		if s := run["step:owner"][0]; s.Status != api.StepStatusDone {
			t.Fatalf("after a skipped step, step:owner = %+v", s)
		}

		// The seeded three-tier item: what it builds, on its form.
		var tt api.RequestForm
		h.json("GET", "/api/v1/catalog/items/three-tier/form?project_id=prj_web", "tnguyen", nil, 200, &tt)
		if tt.Topology == nil {
			t.Fatal("the three-tier item's form has no topology")
		}
		b, _ := json.Marshal(*tt.Topology)
		var tiers struct {
			Kind       string
			Segments   []struct{ Key string }
			Components []struct {
				Key    string
				Count  any
				Groups []string
			}
			SecurityGroups []struct {
				Key   string
				Rules []struct {
					From, Services []string
					Action         string
				}
			} `json:"security_groups"`
		}
		if err := json.Unmarshal(b, &tiers); err != nil {
			t.Fatal(err)
		}
		if tiers.Kind != "vm" || len(tiers.Segments) != 1 || tiers.Segments[0].Key != "app" || len(tiers.Components) != 3 ||
			tiers.Components[0].Count != "inputs.web_nodes" || !slices.Equal(tiers.Components[2].Groups, []string{"db", "nsx:grp-backup-clients"}) ||
			len(tiers.SecurityGroups) != 3 || len(tiers.SecurityGroups[2].Rules) != 2 || tiers.SecurityGroups[2].Rules[1].Action != "drop" {
			t.Fatalf("the three-tier topology = %s", b)
		}
		if strings.Contains(string(b), "corp.example") || strings.Contains(string(b), "naming") {
			t.Fatalf("the three-tier topology shows what only its administrators see: %s", b)
		}

		// Against the NSX Manager the compose stack fakes (rvav fake-nsx):
		// a catalog admin publishes it again unchanged, and it runs.
		fakeNSX := nsx.NewFakeServer()
		fakeNSX.RealizeAfter, fakeNSX.AnyVM, fakeNSX.SeeAfter = 1, true, 1
		fakeNSX.OnSegment = portGroups(t, ctx, c)
		client := useFakeNSX(h, fakeNSX)
		h.acts.NSX, h.acts.NSXTimeout = client, 20*time.Second
		var ttDraft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/three-tier/versions", "lmartin", api.CatalogItemVersionCreate{}, 201, &ttDraft)
		h.json("POST", "/api/v1/admin/catalog/items/three-tier/versions/"+ttDraft.Id+"/publish", "lmartin", nil, 200, nil)
		segmentsInDev(h, admin, "prj_web")
		h.json("GET", "/api/v1/catalog/items/three-tier/form?project_id=prj_web", "tnguyen", nil, 200, &tt)
		var vms api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: tt.ItemId, VersionId: tt.VersionId,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"web_nodes": 2, "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01"}}, 202, &vms, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(vms.Id, nil)
		h.json("GET", "/api/v1/requests/"+vms.Id, "tnguyen", nil, 200, &vms)
		if vms.Status != api.RequestStatusSucceeded {
			t.Fatalf("the three-tier request = %s %s", vms.Status, errorOf(vms))
		}
		var depID string
		if err := h.store.Pool.QueryRow(ctx, `select id from deployments where created_by_request_id = $1`, vms.Id).Scan(&depID); err != nil {
			t.Fatal(err)
		}
		var built []string
		rows, _ := h.store.Pool.Query(ctx, `select name from resources where deployment_id = $1 and type = 'vsphere.vm' order by name`, depID)
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			built = append(built, n)
		}
		rows.Close()
		if !slices.Equal(built, []string{"web-dev-ttapp01", "web-dev-ttdb01", "web-dev-ttweb01", "web-dev-ttweb02"}) {
			t.Fatalf("the three-tier VMs = %v", built)
		}
		// Its groups' rules, the database's drop among them, as NSX took them.
		pol, err := client.SecurityPolicy(ctx, provision.PolicyName(depID))
		if err != nil || len(pol.Rules) != 4 || !slices.ContainsFunc(pol.Rules, func(r nsx.Rule) bool { return r.Action == "DROP" }) {
			t.Fatalf("the three-tier's rules = %+v, %v", pol, err)
		}
	})
}

// segmentsInDev makes the project's development placement rule make NSX
// segments, on the fake NSX's Tier-1 and the development EfficientIP's
// block (10.60.0.0/22), as an administrator sets it in Administration.
func segmentsInDev(h *harness, admin, project string) {
	h.t.Helper()
	var rules api.PlacementRuleList
	h.json("GET", "/api/v1/admin/placement-rules?project_id="+project, admin, nil, 200, &rules)
	r := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })]
	h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, admin, api.PlacementRuleWrite{ProjectId: project, Environment: "dev",
		Priority: &r.Priority, Clusters: r.Clusters, Datastores: r.Datastores, Networks: r.Networks, Folder: r.Folder,
		ClusterTags: &r.ClusterTags, DatastoreTags: &r.DatastoreTags, Segments: &api.PlacementSegments{Tier1: "t1-gva-dev",
			IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example", DnsServers: &[]string{"10.10.0.53"}}}, 200, nil)
}

// compositeNodes are the IDs of the nodes the canvas draws for a
// composite's topology (blueprintGraph): the Request node, a step:<key> per
// step in the order they run, a branch's sides after it (each case's steps,
// then its else's), Done.
func compositeNodes(t *testing.T, topology map[string]any) []string {
	t.Helper()
	out := []string{"inputs"}
	key := func(s any) string { return "step:" + s.(map[string]any)["key"].(string) }
	for _, raw := range topology["steps"].([]any) {
		slot := []any{raw}
		if g, ok := raw.(map[string]any)["parallel"].([]any); ok {
			slot = g
		}
		for _, s := range slot {
			out = append(out, key(s))
			b, _ := s.(map[string]any)["branch"].(map[string]any)
			cases, _ := b["cases"].([]any)
			for _, c := range cases {
				side, _ := c.(map[string]any)["steps"].([]any)
				for _, x := range side {
					out = append(out, key(x))
				}
			}
			otherwise, _ := b["else"].([]any)
			for _, x := range otherwise {
				out = append(out, key(x))
			}
		}
	}
	return append(out, "end")
}

// stepEvent is a request.step event: one step, without its children.
type stepEvent struct {
	Parent *string `json:"parent_step_id"`
	Step   api.Step
}

// followed is a request's page following its run: its steps as it first
// read them, then their changes from its event stream.
type followed struct {
	steps  []api.Step
	events chan stepEvent
}

// follow opens a request's event stream as its page does
// (GET /events?topics=request:<id>), then reads its steps; the stream is
// read until the test ends.
func follow(t *testing.T, h *harness, request, user string) followed {
	t.Helper()
	ctx, cancel := context.WithCancel(h.ctx)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", h.srv.URL+"/api/v1/events?topics=request:"+request, nil)
	req.Header.Set(auth.DevUserHeader, user)
	// Answered once subscribed, at the first heartbeat at the latest.
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("the request's events: %v %v", resp, err)
	}
	var steps struct{ Items []api.Step }
	h.json("GET", "/api/v1/requests/"+request+"/steps", user, nil, 200, &steps)
	out := followed{steps: steps.Items, events: make(chan stepEvent, 4096)}
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		typ := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				typ = v
			} else if v, ok := strings.CutPrefix(line, "data: "); ok && typ == "request.step" {
				var e struct{ Data stepEvent }
				if json.Unmarshal([]byte(v), &e) == nil {
					out.events <- e.Data
				}
			} else if line == "" {
				typ = ""
			}
		}
	}()
	return out
}

// runOnGraph is a finished request's run as its page draws it (run.ts:
// runSteps): its top-level steps by the node of the graph that holds them.
// Every step is on a node: submitted and the approval on the Request node,
// step:<key> on its own, ready on Done. The events the page followed came
// in the graph's order (a step done before the next one starts), and its
// steps as it first read them, changed by those events, are the request's.
func runOnGraph(t *testing.T, h *harness, request, user string, graph []string, page followed) map[string][]api.Step {
	t.Helper()
	nodeOf := func(key string) string {
		switch {
		case key == "submit" || key == "policy":
			return "inputs"
		case key == "ready":
			return "end"
		case slices.Contains(graph, key):
			return key
		}
		return ""
	}
	var timeline struct{ Items []api.Step }
	h.json("GET", "/api/v1/requests/"+request+"/steps", user, nil, 200, &timeline)
	run := map[string][]api.Step{}
	last := map[string]api.Step{}
	for _, s := range timeline.Items {
		id := nodeOf(s.Key)
		if id == "" {
			t.Fatalf("step %s (%s) is on no node of %v", s.Key, s.Title, graph)
		}
		run[id] = append(run[id], s)
		last[s.Id] = s
	}
	for _, id := range graph {
		if len(run[id]) == 0 {
			t.Fatalf("node %s has no step: %v", id, run)
		}
	}

	// What the page saw: the top-level steps' changes, in order.
	var seen []stepEvent
	for _, e := range drainSteps(page.events, 500*time.Millisecond) {
		if e.Parent == nil {
			seen = append(seen, e)
		}
	}
	// When each node started running, and when it first ended.
	first, done := map[string]int{}, map[string]int{}
	final := map[string]api.Step{}
	for _, s := range page.steps {
		final[s.Id] = s
	}
	for i, e := range seen {
		id := nodeOf(e.Step.Key)
		if id == "" {
			t.Fatalf("an event of step %s, on no node of %v", e.Step.Key, graph)
		}
		if _, ok := first[id]; !ok && e.Step.Status == api.StepStatusRunning {
			first[id] = i
		}
		if _, ok := done[id]; !ok && (e.Step.Status == api.StepStatusDone || e.Step.Status == api.StepStatusSkipped) {
			done[id] = i
		}
		final[e.Step.Id] = e.Step
	}
	for _, id := range graph[1 : len(graph)-1] {
		if _, ok := done[id]; !ok {
			t.Fatalf("the page never saw %s end: %v", id, eventKeys(seen))
		}
	}
	for i := 2; i < len(graph)-1; i++ {
		prev, next := graph[i-1], graph[i]
		start, ok := first[next]
		if !ok {
			start = done[next] // skipped without running
		}
		if done[prev] >= start {
			t.Fatalf("%s started (event %d) before %s ended (event %d): %v", next, start, prev, done[prev], eventKeys(seen))
		}
	}
	for id, s := range last {
		if e, ok := final[id]; !ok || e.Status != s.Status {
			t.Fatalf("the page left step %s at %q, the request has it %s", s.Key, e.Status, s.Status)
		}
	}
	return run
}

func drainSteps(c <-chan stepEvent, quiet time.Duration) []stepEvent {
	var out []stepEvent
	for {
		select {
		case e := <-c:
			out = append(out, e)
		case <-time.After(quiet):
			return out
		}
	}
}

func eventKeys(events []stepEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Step.Key + ":" + string(e.Step.Status)
	}
	return out
}
