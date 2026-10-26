package integrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
)

func TestFakeIPAM(t *testing.T) {
	ctx := context.Background()
	f := efficientip.NewFake()
	f.InUse = func(context.Context, string) ([]string, error) { return []string{"10.31.10.20"}, nil }
	s := efficientip.Subnet{CIDR: "10.31.10.0/24", Gateway: "10.31.10.1"}

	next, err := f.NextFree(ctx, s)
	if err != nil || next != "10.31.10.21" {
		t.Fatalf("next free = %q, %v (want .21, .20 is in use)", next, err)
	}
	a, err := f.Reserve(ctx, s, "web-dev-app001")
	if err != nil || a.IP != "10.31.10.21" {
		t.Fatalf("reserve = %+v, %v", a, err)
	}
	again, _ := f.Reserve(ctx, s, "web-dev-app001")
	if again.IP != a.IP {
		t.Fatalf("reserving the same host twice gave %s then %s", a.IP, again.IP)
	}
	if err := f.Release(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CreateRecord(ctx, efficientip.Record{FQDN: "web-dev-app001.corp.example", Type: "A", Value: a.IP}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.NameExists(ctx, "web-dev-app001.corp.example"); !ok {
		t.Fatal("record not found")
	}
	if _, err := f.CreateRecord(ctx, efficientip.Record{FQDN: "web-dev-app001.corp.example", Type: "A", Value: "10.0.0.1"}); err == nil {
		t.Fatal("conflicting A record should fail")
	}
	if got := efficientip.PTRName("10.31.10.21"); got != "21.10.31.10.in-addr.arpa" {
		t.Fatalf("ptr = %s", got)
	}
	small := efficientip.Subnet{CIDR: "10.0.0.0/30", Gateway: "10.0.0.1"}
	f.FirstHost = 1
	if _, err := f.Reserve(ctx, small, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Reserve(ctx, small, "b"); err != efficientip.ErrSubnetFull {
		t.Fatalf("full subnet = %v", err)
	}
}

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
