package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/vra"
	"github.com/damhau/rvav/internal/vraimport"
)

// askRequester is std-ask-requester rebuilt as a composite (D2.6), as the
// designer writes its steps into the YAML: the requester is asked which
// backup policy the VM needs, the answer is recorded in the CMDB, and a branch
// on it tells the owner when there will be no backup. The browser's route
// builds the same steps (web/e2e/d2-acceptance.spec.ts).
const askRequester = `
- key: detail
  title: Ask for the backup policy
  ask:
    requester: true
    message: Which backup policy does this VM need?
    form:
      - key: backup
        label: Backup policy
        type: choice
        options: [daily, weekly, none]
        required: true
- key: record
  title: Record it in the CMDB
  rest:
    method: PATCH
    url: https://cmdb.corp.example/api/ci/${inputs.name}
    body: "{'backup': steps.detail.answer.backup}"
- key: way
  title: Backed up or not
  branch:
    cases:
      - title: No backup
        when: steps.detail.answer.backup == 'none'
        steps:
          - key: warn
            title: Warn the owner
            notify:
              owner: true
              message: "${inputs.name} has backup policy ${steps.detail.answer.backup}: nothing will back it up."
`

// TestD2Acceptance is the composite step kinds' acceptance (D2.6, ADR-0033),
// through the API the pages use and a real Temporal dev server. The vRO
// workflow picked from rvav vro-triage -standard, with a user interaction and
// a REST call, is rebuilt as a new composite item, published and requested.
// Its requester answers while no worker is up, and the run goes on when one
// is back: the CMDB receives the answer, and the branch on it takes its side,
// whose message to the owner says what was answered. The page follows the run
// on the form's graph, the side included. Answered the other way, the side is
// skipped and nobody is told.
func TestD2Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	// Picked from the triage: group A, a REST call and a wait for a person.
	estate := vra.StandardVRO(time.Now())
	triage := vraimport.TriageVRO(&estate)
	i := slices.IndexFunc(triage.Items, func(it vraimport.TriageItem) bool { return it.ID == "std-ask-requester" })
	if i < 0 {
		t.Fatal("the triage has no std-ask-requester")
	}
	picked := triage.Items[i]
	if picked.Group != vraimport.GroupNative || !slices.Equal(picked.Steps, []string{vraimport.StepREST, vraimport.StepPerson}) {
		t.Fatalf("std-ask-requester's triage = %+v", picked)
	}

	ts := startTemporal(t, "--dynamic-config-value", "limit.historyCount.suggestContinueAsNew=5")
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester, queue = "mkeller", "tnguyen", "rvav-d2-e2e"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		cmdb := newRESTCMDB(t)
		h.svc.REST.Hosts = []string{cmdb.URL}
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		worker := func() func() {
			w := provision.NewWorker(tc, queue, h.acts)
			if err := w.Start(); err != nil {
				t.Fatal(err)
			}
			return w.Stop
		}
		stop := func() {}
		defer func() { stop() }()

		// Rebuilt: a new composite item, its steps as the designer writes
		// them, saved, published and offered to Web Platform.
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "ask-requester", Name: "Ask the requester",
			Summary: "Asks the requester which backup policy a VM needs.", Category: "compute", Type: api.Composite}, 201, &item)
		var draft api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/ask-requester/versions/"+item.DraftVersion.Id, admin, nil, 200, &draft)
		var steps []any
		if err := yaml.Unmarshal([]byte(strings.ReplaceAll(askRequester, "https://cmdb.corp.example", cmdb.URL)), &steps); err != nil {
			t.Fatal(err)
		}
		def := draft.Definition
		def.Blueprint["steps"] = steps
		h.json("PUT", "/api/v1/admin/catalog/items/ask-requester/versions/"+draft.Id, admin,
			api.CatalogItemVersionUpdate{Definition: def, BaseRevision: &draft.Revision}, 200, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/ask-requester/versions/"+draft.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/ask-requester/entitlements", admin,
			api.CatalogEntitlements{ProjectIds: []string{"prj_web"}}, 200, nil)

		// The form draws the steps the run will, the branch's side after it;
		// what its case compares to and what the message says stay out.
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/ask-requester/form?project_id=prj_web", requester, nil, 200, &form)
		if form.Topology == nil {
			t.Fatal("the form has no topology")
		}
		graph := compositeNodes(t, *form.Topology)
		if !slices.Equal(graph, []string{"inputs", "step:detail", "step:record", "step:way", "step:warn", "end"}) {
			t.Fatalf("the form's graph = %v", graph)
		}
		if b, _ := json.Marshal(form.Topology); strings.Contains(string(b), "'none'") || strings.Contains(string(b), "back it up") {
			t.Fatalf("the topology shows a case's literal or a message: %s", b)
		}

		// Requested; its page follows it from the start.
		d := api.RequestDraft{CatalogItemId: form.ItemId, VersionId: form.VersionId, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"name": "web-01"}}
		req := rig.submit(requester, d)
		page := follow(t, h, req.Id, requester)
		stop = worker()
		asked := h.waitInbox(requester, 1).Items[0]
		if asked.StepKey == nil || *asked.StepKey != "step:detail" || asked.Request.Id != req.Id {
			t.Fatalf("the requester is asked %+v", asked)
		}
		if len(cmdb.seen("PATCH", "/api/ci/")) != 0 {
			t.Fatal("the CMDB was called before the answer")
		}

		// The requester answers while no worker is up; the run goes on when one is back.
		stop()
		rig.answer(asked.Id, requester, 200, map[string]any{"backup": "none"}, nil)
		if r := rig.get(req.Id, requester); r.Status != api.RequestStatusRunning {
			t.Fatalf("with no worker = %s", r.Status)
		}
		stop = worker()
		h.waitStatus(req.Id, requester, api.RequestStatusSucceeded)
		patched := cmdb.seen("PATCH", "/api/ci/")
		if len(patched) != 1 || patched[0].path != "/api/ci/web-01" || patched[0].body["backup"] != "none" {
			t.Fatalf("what the CMDB received = %+v", patched)
		}

		// Followed on the graph: every step on its node, in order, the side taken included.
		run := runOnGraph(t, h, req.Id, requester, graph, page)
		way, warn := run["step:way"][0], run["step:warn"][0]
		if way.Status != api.StepStatusDone || way.BranchCase == nil || *way.BranchCase != 1 || way.Detail == nil || *way.Detail != "Took: No backup" {
			t.Fatalf("the branch = %+v", way)
		}
		if warn.Status != api.StepStatusDone {
			t.Fatalf("its side = %+v", warn)
		}
		var said string
		if err := h.store.Pool.QueryRow(ctx, `select body from notifications where request_id = $1 and kind = 'step_message' and user_id = $2`,
			req.Id, seed.UserID(requester)).Scan(&said); err != nil {
			t.Fatalf("the owner's message: %v", err)
		}
		if said != "web-01 has backup policy none: nothing will back it up." {
			t.Fatalf("the owner was told %q", said)
		}

		// Answered the other way: the CMDB records it, and the side is skipped.
		other := rig.submit(requester, d)
		asked = h.waitInbox(requester, 1).Items[0]
		rig.answer(asked.Id, requester, 200, map[string]any{"backup": "daily"}, nil)
		h.waitStatus(other.Id, requester, api.RequestStatusSucceeded)
		if patched = cmdb.seen("PATCH", "/api/ci/"); len(patched) != 2 || patched[1].body["backup"] != "daily" {
			t.Fatalf("what the CMDB received = %+v", patched)
		}
		st := rig.steps(other.Id, requester)
		if s := st["step:way"]; s.BranchCase == nil || *s.BranchCase != 0 || s.Detail == nil || *s.Detail != "No case held: nothing ran here." {
			t.Fatalf("answered daily, the branch = %+v", s)
		}
		if s := st["step:warn"]; s.Status != api.StepStatusSkipped {
			t.Fatalf("answered daily, its side = %+v", s)
		}
		if n := rig.count(`select count(*) from notifications where request_id = $1 and kind = 'step_message'`, other.Id); n != 0 {
			t.Fatalf("answered daily, %d told", n)
		}
	})
}
