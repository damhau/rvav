package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
)

// TestPowerShell runs testdata/rvav-module.ps1 with the PowerShell module
// (O1.2) when pwsh is on PATH: connecting with RVAV_URL and RVAV_TOKEN,
// the catalog, a request and its wait, a deployment's day-2 action and
// delete, and approvals decided with an API token.
func TestPowerShell(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell 7 (pwsh) isn't on PATH")
	}
	module, err := filepath.Abs("../../powershell/Rvav/Rvav.psd1")
	if err != nil {
		t.Fatal(err)
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		var lea, marc api.ApiTokenCreated
		h.json("POST", "/api/v1/tokens", "lmartin", api.ApiTokenCreate{Name: "powershell"}, 201, &lea)
		h.json("POST", "/api/v1/tokens", "mkeller", api.ApiTokenCreate{Name: "powershell"}, 201, &marc)
		cmd := exec.CommandContext(ctx, pwsh, "-NoProfile", "-NonInteractive", "-File", "testdata/rvav-module.ps1")
		cmd.Env = append(os.Environ(), "RVAV_URL="+h.srv.URL, "RVAV_TOKEN="+lea.Token, "RVAV_APPROVER_TOKEN="+marc.Token,
			"RVAV_MODULE="+module)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		n := h.wf.startedCount()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		// next is the script's next request, unless the script ends first.
		next := func() string {
			deadline := time.Now().Add(time.Minute)
			for time.Now().Before(deadline) {
				select {
				case err := <-exited:
					t.Fatalf("the script ended (%v) before its request %d:\n%s", err, n+1, out.String())
				default:
				}
				if h.wf.startedCount() > n {
					n++
					return h.wf.nextStarted(t, n-1)
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("no request %d from the script:\n%s", n+1, out.String())
			return ""
		}
		// decided waits for Marc's decision, then gives it to the workflow.
		decided := func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				deadline := time.Now().Add(time.Minute)
				for time.Now().Before(deadline) {
					h.wf.mu.Lock()
					sent := len(h.wf.signals)
					h.wf.mu.Unlock()
					if sent > 0 {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				h.wf.deliver(env)
			}, time.Hour)
		}
		// The dev request, power off, the production requests approved and
		// rejected, the delete.
		for _, setup := range []func(*testsuite.TestWorkflowEnvironment){nil, nil, decided, decided, nil} {
			h.runWorkflow(next(), setup)
		}
		if err := <-exited; err != nil {
			t.Fatalf("the script: %v\n%s", err, out.String())
		}
		mustContain(t, "the script", out.String(), "PowerShell script done")
		t.Logf("pwsh ran the module's script:\n%s", out.String())
	})
}
