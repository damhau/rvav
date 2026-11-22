package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/cli"
	"github.com/damhau/rvav/internal/seed"
)

// rvavctl is the command line on one user's machine: their profiles file,
// their environment and their browser.
type rvavctl struct {
	t       *testing.T
	ctx     context.Context
	config  string
	env     map[string]string
	browser func(url string) error
}

func newRvavctl(t *testing.T, ctx context.Context) *rvavctl {
	return &rvavctl{t: t, ctx: ctx, config: filepath.Join(t.TempDir(), "rvav", "config.json"), env: map[string]string{}}
}

// ran is what one run of rvavctl did.
type ran struct {
	code        int
	out, stderr string
}

func (c *rvavctl) try(stdin string, args ...string) ran {
	var out, stderr bytes.Buffer
	code := cli.Main(c.ctx, args, cli.Env{In: strings.NewReader(stdin), Out: &out, Err: &stderr,
		Getenv: func(k string) string { return c.env[k] }, ConfigPath: c.config, OpenBrowser: c.browser,
		HTTP: &http.Client{Timeout: time.Minute}, Poll: 20 * time.Millisecond})
	return ran{code, out.String(), stderr.String()}
}

// run expects rvavctl to exit with want and returns its standard output and
// error.
func (c *rvavctl) run(want int, stdin string, args ...string) (string, string) {
	c.t.Helper()
	r := c.try(stdin, args...)
	if r.code != want {
		c.t.Fatalf("rvavctl %s = %d, want %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, want, r.out, r.stderr)
	}
	return r.out, r.stderr
}

func (c *rvavctl) json(out any, args ...string) {
	c.t.Helper()
	stdout, _ := c.run(0, "", append(args, "-o", "json")...)
	if err := json.Unmarshal([]byte(stdout), out); err != nil {
		c.t.Fatalf("rvavctl %s: %v: %s", strings.Join(args, " "), err, stdout)
	}
}

// start runs rvavctl in the background, for --wait.
func (c *rvavctl) start(args ...string) <-chan ran {
	done := make(chan ran, 1)
	go func() { done <- c.try("", args...) }()
	return done
}

func mustContain(t *testing.T, what, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("%s has no %q:\n%s", what, w, got)
		}
	}
}

// nextStarted is the workflow started after the first n.
func (r *recorder) nextStarted(t *testing.T, n int) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		r.mu.Lock()
		started := slices.Clone(r.started)
		r.mu.Unlock()
		if len(started) > n {
			return started[n]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no workflow started after %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *recorder) startedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.started)
}

// TestCLI runs rvavctl against an rvav in development mode, as O1.1's
// acceptance: signing in, the catalog, requesting with inputs from flags
// and a file, waiting for requests, day-2 actions, approvals with an API
// token, failures, retry, cancel and signing out.
func TestCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		lea := newRvavctl(t, ctx)

		_, stderr := lea.run(4, "", "whoami")
		mustContain(t, "whoami before login", stderr, "not signed in", "rvavctl login --server")

		// Development mode signs everyone in as one user.
		out, _ := lea.run(0, "", "login", "--server", h.srv.URL)
		mustContain(t, "login", out, "Signed in to "+h.srv.URL+" as Léa Martin ("+seed.UserID("lmartin")+")")
		var me api.Me
		lea.json(&me, "whoami")
		if me.User.Name != "Léa Martin" || len(me.Projects) != 1 || me.Projects[0].Project.Id != "prj_web" {
			t.Fatalf("whoami = %+v", me)
		}
		out, _ = lea.run(0, "", "whoami")
		mustContain(t, "whoami", out, "Name:", "Léa Martin", "prj_web", "Project admin")

		// The catalog: Léa requests in one project only, so no --project.
		var items []api.CatalogItem
		lea.json(&items, "catalog", "list")
		if !slices.ContainsFunc(items, func(i api.CatalogItem) bool { return i.Slug == "rhel9-server" }) {
			t.Fatalf("catalog = %+v", items)
		}
		lea.json(&items, "catalog", "list", "--limit", "2")
		if len(items) != 2 {
			t.Fatalf("catalog --limit 2 = %d items", len(items))
		}
		out, _ = lea.run(0, "", "catalog", "show", "rhel9-server", "--project", "web")
		mustContain(t, "catalog show", out, "Approval:", "conditional", "INPUT", "role", "post_config", "list of")

		// A dry run says what rvav would refuse, and fails.
		out, stderr = lea.run(1, "", "catalog", "request", "rhel9-server", "--env", "dev", "--input", "role=App!", "--dry-run")
		mustContain(t, "refused dry run", out, "Valid:", "no")
		mustContain(t, "refused dry run", stderr, "rvav would refuse this request", "role")

		// Inputs from a file, and flags over it.
		file := filepath.Join(t.TempDir(), "server.yaml")
		if err := os.WriteFile(file, []byte("role: web\nsize: M\ncluster: GVA-DEV-CL01\ndatastore: vsan-gva-dev-cl01\n"+
			"network: dvpg-app-dev-vl310\npost_config: [linux-baseline]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		request := []string{"catalog", "request", "rhel9-server", "--env", "dev", "--inputs", file, "--input", "role=app",
			"--input", "post_config=linux-baseline,monitoring-agent", "--justification", "Load test, WEB-2291."}
		out, _ = lea.run(0, "", append(request, "--dry-run")...)
		mustContain(t, "dry run", out, "Valid:", "yes", "web-dev-app001", "not needed")

		// --wait follows the request until its workflow ends.
		n := h.wf.startedCount()
		done := lea.start(append(request, "--wait", "-o", "json")...)
		h.runWorkflow(h.wf.nextStarted(t, n), nil)
		res := <-done
		var req api.Request
		if res.code != 0 || json.Unmarshal([]byte(res.out), &req) != nil || req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request --wait = %d\n%s\n%s", res.code, res.out, res.stderr)
		}
		mustContain(t, "request --wait's progress", res.stderr, req.Number+" ("+req.Id+")", req.Number+" succeeded")
		var inputs map[string]any
		if b, _ := json.Marshal(req.Inputs); json.Unmarshal(b, &inputs) != nil || inputs["role"] != "app" ||
			fmt.Sprint(inputs["post_config"]) != "[linux-baseline monitoring-agent]" || inputs["size"] != "M" {
			t.Fatalf("request inputs = %v", inputs)
		}
		dep := (*req.DeploymentIds)[0]

		var reqs []api.Request
		lea.json(&reqs, "requests", "list", "--mine", "--status", "succeeded")
		if len(reqs) != 1 || reqs[0].Id != req.Id {
			t.Fatalf("requests list = %+v", reqs)
		}
		out, _ = lea.run(0, "", "requests", "show", req.Number)
		mustContain(t, "requests show", out, "Request:", req.Number, "succeeded", "STEP")
		var shown struct {
			Request api.Request `json:"request"`
			Steps   []api.Step  `json:"steps"`
		}
		lea.json(&shown, "requests", "show", req.Id)
		if shown.Request.Id != req.Id || len(shown.Steps) == 0 {
			t.Fatalf("requests show -o json = %+v", shown)
		}

		// Deployments and their day-2 actions.
		var deps []api.Deployment
		lea.json(&deps, "deployments", "list", "--mine", "--env", "dev")
		if len(deps) != 1 || deps[0].Id != dep {
			t.Fatalf("deployments list = %+v", deps)
		}
		out, _ = lea.run(0, "", "deployments", "show", dep)
		mustContain(t, "deployments show", out, "Deployment:", "RESOURCE", "web-dev-app001", "vCPU")
		out, _ = lea.run(0, "", "deployments", "actions", dep)
		mustContain(t, "deployments actions", out, "ACTION", "power_off", "resize", "web-dev-app001")
		out, _ = lea.run(0, "", "deployments", "actions", dep, "resize")
		mustContain(t, "deployments actions resize", out, "Resize", "INPUT", "cpu_count", "memory_gb", "integer")
		var action map[string]any
		lea.json(&action, "deployments", "actions", dep, "resize")
		if action["key"] != "resize" || action["form"] == nil {
			t.Fatalf("deployments actions resize -o json: %v", action)
		}

		_, stderr = lea.run(2, "", "deployments", "run", dep, "power_off")
		mustContain(t, "power_off without --yes", stderr, "--yes")
		n = h.wf.startedCount()
		done = lea.start("deployments", "run", dep, "power_off", "--resource", "web-dev-app001", "--yes", "--wait")
		h.runWorkflow(h.wf.nextStarted(t, n), nil)
		if res = <-done; res.code != 0 {
			t.Fatalf("power_off --wait = %d\n%s\n%s", res.code, res.out, res.stderr)
		}
		mustContain(t, "power_off --wait", res.out, "Power off", "succeeded")

		// The resize form starts from the VM's size: what isn't given keeps it.
		var resize api.Request
		lea.json(&resize, "deployments", "run", dep, "resize", "--input", "cpu_count=6", "--yes")
		var resized struct {
			Inputs map[string]any `json:"inputs"`
		}
		h.json("GET", "/api/v1/requests/"+resize.Id, "lmartin", nil, 200, &resized)
		if resized.Inputs["cpu_count"] != float64(6) || resized.Inputs["memory_gb"] == nil {
			t.Fatalf("resize inputs = %v", resized.Inputs)
		}
		h.runWorkflow(resize.Id, nil)

		// Marc approves with an API token: approvers name him.
		var made api.ApiTokenCreated
		h.json("POST", "/api/v1/tokens", "mkeller", api.ApiTokenCreate{Name: "rvavctl"}, 201, &made)
		marc := newRvavctl(t, ctx)
		out, _ = marc.run(0, made.Token+"\n", "login", "--server", h.srv.URL+"/api/v1/", "--with-token", "--profile", "work")
		mustContain(t, "login --with-token", out, "as Marc Keller", "with an API token")
		if fi, err := os.Stat(marc.config); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("profiles file: %v %v", fi.Mode(), err)
		}
		prd := []string{"catalog", "request", "rhel9-server", "--env", "prd", "--input", "role=front", "--input", "size=XL",
			"--input", "cluster=GVA-PRD-CL02", "--input", "datastore=vsan-gva-prd-cl02", "--input", "network=dvpg-web-prd-vl210",
			"--input", "post_config=linux-baseline", "--justification", "Fourth front node for Black Friday."}
		out, _ = lea.run(0, "", append(prd, "--dry-run")...)
		mustContain(t, "prd dry run", out, "Approval:", "needed")
		var waiting api.Request
		lea.json(&waiting, prd...)
		if waiting.Status != api.RequestStatusPendingApproval {
			t.Fatalf("prd request = %s", waiting.Status)
		}
		var inbox, show, noComment, approve ran
		h.runWorkflow(waiting.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				inbox = marc.try("", "approvals", "list", "--profile", "work", "-o", "json")
				var items []api.ApprovalSummary
				if json.Unmarshal([]byte(inbox.out), &items) != nil || len(items) != 1 {
					return
				}
				show = marc.try("", "approvals", "show", items[0].Id, "--profile", "work")
				noComment = marc.try("", "approvals", "reject", items[0].Id, "--profile", "work")
				approve = marc.try("", "approvals", "approve", items[0].Id, "--profile", "work", "-m", "OK for Black Friday")
				h.wf.deliver(env)
			}, time.Hour)
		})
		mustContain(t, "approvals list", inbox.out, waiting.Number)
		mustContain(t, "approvals show", show.out, "You may decide:", "yes", "Fourth front node")
		if noComment.code != 2 || approve.code != 0 {
			t.Fatalf("reject without comment = %d %s, approve = %d %s", noComment.code, noComment.stderr, approve.code, approve.stderr)
		}
		mustContain(t, "approve", approve.out, "approved")
		out, _ = lea.run(0, "", "requests", "wait", waiting.Number, "--timeout", "1m")
		mustContain(t, "approved request", out, "succeeded")

		// Scripts give the address and the token in the environment.
		script := newRvavctl(t, ctx)
		script.env = map[string]string{"RVAV_URL": h.srv.URL, "RVAV_TOKEN": made.Token}
		script.json(&me, "whoami")
		if me.User.Id != seed.UserID("mkeller") {
			t.Fatalf("RVAV_TOKEN acts as %s", me.User.Id)
		}

		// A request that fails fails --wait, with why; it can be retried.
		h.awx.FailTask = "idm : join realm"
		var failing api.Request
		lea.json(&failing, "catalog", "request", "rhel9-server", "--env", "dev", "--inputs", file, "--input", "role=worker",
			"--input", "size=S", "--input", "post_config=linux-baseline,idm-join")
		h.runWorkflow(failing.Id, nil)
		h.awx.FailTask = ""
		_, stderr = lea.run(1, "", "requests", "wait", failing.Number)
		mustContain(t, "failed request's wait", stderr, failing.Number+" failed", "idm : join realm")
		out, _ = lea.run(0, "", "requests", "retry", failing.Number)
		mustContain(t, "retry", out, failing.Number)
		_, stderr = script.run(1, `{"by": "a script"}`, "requests", "signal", failing.Number, "cab-approved", "--data", "-")
		mustContain(t, "a signal nothing waits for", stderr, "signal")
		_, stderr = script.run(2, "", "requests", "signal", failing.Number, "cab-approved", "--data", "[1]")
		mustContain(t, "a signal that isn't an object", stderr, "--data: a JSON object")

		// Cancel a request waiting for approval.
		var edge api.Request
		lea.json(&edge, append(slices.Clone(prd), "--input", "role=edge")...)
		var cancelled api.Request
		lea.json(&cancelled, "requests", "cancel", edge.Number)
		if cancelled.Status != api.RequestStatusCancelled && cancelled.Status != api.RequestStatusCancelling {
			t.Fatalf("cancelled = %s", cancelled.Status)
		}

		// A list that shows only the first requests says so.
		lea.json(&reqs, "requests", "list", "--mine", "--limit", "1")
		_, stderr = lea.run(0, "", "requests", "list", "--mine", "--limit", "1")
		mustContain(t, "requests list --limit 1", stderr, "Showing the first 1 requests; there are more")
		lea.json(&reqs, "requests", "list", "--mine", "--all")
		if len(reqs) < 4 {
			t.Fatalf("requests list --all = %d requests", len(reqs))
		}

		out, _ = lea.run(0, "", "logout")
		mustContain(t, "logout", out, "Signed out of "+h.srv.URL)
		lea.run(4, "", "whoami")
	})
}
