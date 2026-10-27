package integrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/integrations/awx"
)

func TestFakeAWXJob(t *testing.T) {
	ctx := context.Background()
	f := awx.NewFake(5*time.Millisecond, "idm : join realm")
	id, err := f.LookupJobTemplate(ctx, "idm-join", false)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.Launch(ctx, id, false, awx.LaunchRequest{Limit: "web-dev-app001"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if job, _ = f.GetJob(ctx, job.ID, false); job.Status != "failed" || !job.Failed {
		t.Fatalf("job = %+v", job)
	}
	evs, _ := f.Events(ctx, job.ID, 0)
	var failed, recap bool
	for _, e := range evs {
		if e.Event == "runner_on_failed" && e.Task == "idm : join realm" && strings.Contains(e.Message, "ipa-client-install") {
			failed = true
		}
		if e.Event == "playbook_on_stats" && strings.Contains(e.Stdout, "failed=1") {
			recap = true
		}
	}
	if !failed || !recap {
		t.Fatalf("events = %+v", evs)
	}
	if later, _ := f.Events(ctx, job.ID, evs[len(evs)-1].Counter); len(later) != 0 {
		t.Fatalf("events after the last counter = %+v", later)
	}
	if awx.LineKind("changed: [web-dev-app001]") != "changed" || awx.LineKind("PLAY RECAP ****") != "recap" {
		t.Fatal("line kinds")
	}
}
