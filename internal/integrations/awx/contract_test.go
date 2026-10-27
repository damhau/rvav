package awx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// The contract tests run the client against a real AWX with rvav's test
// content (deploy/awx/README.md). They are skipped unless
// RVAV_AWX_TEST_URL and RVAV_AWX_TEST_TOKEN are set.
func contractClient(t *testing.T) (*HTTPClient, context.Context) {
	t.Helper()
	url, token := os.Getenv("RVAV_AWX_TEST_URL"), os.Getenv("RVAV_AWX_TEST_TOKEN")
	if url == "" || token == "" {
		t.Skip("set RVAV_AWX_TEST_URL and RVAV_AWX_TEST_TOKEN to run against a test AWX")
	}
	c, err := NewHTTPClient(url, token, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	content, err := LoadContent("../../../deploy/awx/content.yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(ctx, c, content, Rvav{}, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	c.Organization = content.Organization
	return c, ctx
}

func wait(t *testing.T, ctx context.Context, c *HTTPClient, id int, workflow bool, until func(Job) bool) Job {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		j, err := c.GetJob(ctx, id, workflow)
		if err != nil {
			t.Fatal(err)
		}
		if until(j) {
			return j
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("job %d did not get there in time", id)
	return Job{}
}

func finished(j Job) bool { return j.Finished }

func launch(t *testing.T, ctx context.Context, c *HTTPClient, name string, workflow bool, req LaunchRequest) Job {
	t.Helper()
	id, err := c.LookupJobTemplate(ctx, name, workflow)
	if err != nil {
		t.Fatal(err)
	}
	j, err := c.Launch(ctx, id, workflow, req)
	if err != nil {
		t.Fatalf("launch %s: %v", name, err)
	}
	return j
}

func TestContractTemplates(t *testing.T) {
	c, ctx := contractClient(t)
	all, err := c.Templates(ctx, TemplateFilter{Organization: "rvav-test"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tpl := range all {
		names = append(names, tpl.Name)
		if tpl.Organization != "rvav-test" || !tpl.AskLimit || !tpl.AskVariables || tpl.Modified.IsZero() {
			t.Errorf("template %+v", tpl)
		}
	}
	for _, want := range []string{"linux-baseline", "install-monitoring-agent", "dns-record", "sleep", "configure-vm"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s missing from %v", want, names)
		}
	}
	day2, err := c.Templates(ctx, TemplateFilter{Organization: "rvav-test", Labels: []string{"day2"}})
	if err != nil || len(day2) != 1 || day2[0].Name != "install-monitoring-agent" || !slices.Contains(day2[0].Labels, "rvav") {
		t.Fatalf("label day2 = %+v, %v", day2, err)
	}
	both, _ := c.Templates(ctx, TemplateFilter{Organization: "rvav-test", Labels: []string{"rvav", "day2"}})
	if n := len(both); n != 5 { // linux-baseline, idm-join, install-monitoring-agent, dns-record, configure-vm
		t.Fatalf("labels rvav or day2 = %d templates", n)
	}

	survey, err := c.Survey(ctx, day2[0].ID, false)
	if err != nil || len(survey) != 4 {
		t.Fatalf("survey = %+v, %v", survey, err)
	}
	if q := survey[0]; q.Variable != "monitoring_agent" || q.Type != "multiplechoice" || !slices.Equal(q.Choices, []string{"node_exporter", "telegraf"}) {
		t.Fatalf("first question = %+v", q)
	}
	if q := survey[2]; q.Type != "integer" || q.Min == nil || *q.Min != 1024 || q.Default != float64(9100) {
		t.Fatalf("port question = %+v", q)
	}
	sleep := all[slices.IndexFunc(all, func(t Template) bool { return t.Name == "sleep" })]
	if s, err := c.Survey(ctx, sleep.ID, false); err != nil || len(s) != 0 {
		t.Fatalf("no survey = %+v, %v", s, err)
	}

	if _, err := c.LookupJobTemplate(ctx, "no-such-template", false); err == nil || !strings.Contains(err.Error(), "not found in organization rvav-test") {
		t.Fatalf("unknown template = %v", err)
	}
}

func TestContractInventoriesAndCredentials(t *testing.T) {
	c, ctx := contractClient(t)
	inventories, err := c.Inventories(ctx, "rvav-test")
	if err != nil || !slices.ContainsFunc(inventories, func(i Inventory) bool { return i.Name == "rvav-test-localhost" && i.ID != 0 }) {
		t.Fatalf("inventories = %+v, %v", inventories, err)
	}
	all, err := c.Credentials(ctx, "rvav-test", "")
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := c.Credentials(ctx, "rvav-test", "cloud")
	if err != nil || len(cloud) > len(all) || slices.ContainsFunc(cloud, func(c Credential) bool { return c.Kind != "cloud" }) {
		t.Fatalf("cloud credentials = %+v (of %+v), %v", cloud, all, err)
	}
}

func TestContractJobs(t *testing.T) {
	c, ctx := contractClient(t)

	// A successful job, with rvav's variables, a limit and an inventory
	// other than the template's; its events.
	inventory, err := c.find(ctx, "/inventories/", "rvav-test-localhost", 0)
	if err != nil || inventory == 0 {
		t.Fatalf("inventory rvav-test-localhost = %d, %v", inventory, err)
	}
	j := launch(t, ctx, c, "linux-baseline", false, LaunchRequest{Limit: "localhost", Inventory: inventory,
		ExtraVars: map[string]any{"rvav": map[string]any{"request_id": "req_contract", "project": "web"}}})
	if j.ID == 0 || !strings.Contains(j.URL, "/jobs/playbook/") {
		t.Fatalf("launched %+v", j)
	}
	if j = wait(t, ctx, c, j.ID, false, finished); j.Status != "successful" || j.Failed {
		t.Fatalf("baseline = %+v", j)
	}
	evs, err := c.Events(ctx, j.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []string
	var out strings.Builder
	for _, e := range evs {
		if e.Event == "playbook_on_task_start" {
			tasks = append(tasks, e.Task)
		}
		out.WriteString(e.Stdout)
	}
	if !slices.Equal(tasks, []string{"Show what rvav sent", "Harden SSH", "Configure time sync"}) ||
		!strings.Contains(out.String(), "localhost for request req_contract in web") || evs[len(evs)-1].Event != "playbook_on_stats" {
		t.Fatalf("events: tasks %v, output %s", tasks, out.String())
	}
	if more, err := c.Events(ctx, j.ID, evs[len(evs)-1].Counter); err != nil || len(more) != 0 {
		t.Fatalf("events after the last = %d, %v", len(more), err)
	}

	// A failed task, with its message.
	f := launch(t, ctx, c, "install-monitoring-agent", false, LaunchRequest{Limit: "localhost",
		ExtraVars: map[string]any{"fail_on_purpose": "true"}})
	if f = wait(t, ctx, c, f.ID, false, finished); f.Status != "failed" || !f.Failed {
		t.Fatalf("failing job = %+v", f)
	}
	evs, _ = c.Events(ctx, f.ID, 0)
	if i := slices.IndexFunc(evs, func(e Event) bool { return e.Event == "runner_on_failed" }); i < 0 ||
		evs[i].Message != "The agent did not start on localhost" || evs[i].Task != "Fail when asked to" {
		t.Fatalf("no failed event with its message in %+v", evs)
	}

	// A survey answer AWX refuses.
	id, _ := c.LookupJobTemplate(ctx, "install-monitoring-agent", false)
	_, err = c.Launch(ctx, id, false, LaunchRequest{Limit: "localhost", ExtraVars: map[string]any{"listen_port": 80}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(apiErr.Message, "listen_port") {
		t.Fatalf("a port below the survey's minimum = %v", err)
	}

	// A template that doesn't prompt for an inventory is not launched on another.
	sleep, _ := c.LookupJobTemplate(ctx, "sleep", false)
	if _, err := c.Launch(ctx, sleep, false, LaunchRequest{Limit: "localhost", Inventory: inventory}); !errors.Is(err, ErrNoInventoryPrompt) {
		t.Fatalf("launching sleep on another inventory = %v", err)
	}

	// Cancel.
	s := launch(t, ctx, c, "sleep", false, LaunchRequest{Limit: "localhost", ExtraVars: map[string]any{"sleep_seconds": 300}})
	wait(t, ctx, c, s.ID, false, func(j Job) bool { return j.Status == "running" })
	if err := c.Cancel(ctx, s.ID, false); err != nil {
		t.Fatal(err)
	}
	if s = wait(t, ctx, c, s.ID, false, finished); s.Status != "canceled" {
		t.Fatalf("cancelled job = %+v", s)
	}
}

func TestContractWorkflow(t *testing.T) {
	c, ctx := contractClient(t)
	w := launch(t, ctx, c, "configure-vm", true, LaunchRequest{Limit: "localhost"})
	if !strings.Contains(w.URL, "/jobs/workflow/") {
		t.Fatalf("workflow URL = %s", w.URL)
	}
	if w = wait(t, ctx, c, w.ID, true, finished); w.Status != "successful" {
		t.Fatalf("workflow = %+v", w)
	}
	nodes, err := c.WorkflowNodes(ctx, w.ID)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("nodes = %+v, %v", nodes, err)
	}
	for i, want := range []string{"linux-baseline", "install-monitoring-agent"} {
		if n := nodes[i]; n.Name != want || n.JobID == 0 || n.Status != "successful" || n.DoNotRun {
			t.Fatalf("node %d = %+v", i, n)
		}
	}
	if evs, err := c.Events(ctx, nodes[1].JobID, 0); err != nil || len(evs) == 0 {
		t.Fatalf("events of a node's job = %d, %v", len(evs), err)
	}
}
