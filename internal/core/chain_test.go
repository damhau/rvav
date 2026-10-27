package core

import (
	"testing"
	"time"

	"github.com/damhau/rvav/internal/store"
)

func TestAddLevelMergesSameApprovers(t *testing.T) {
	leads := level{Name: "Infrastructure leads", Group: "rvav-infra-leads", Approvers: []string{"usr_mkeller"}}
	big := store.ApprovalPolicy{ID: "pol_big", Reason: "Production VM above 32 GB RAM", Condition: "vm.memory_gb > 32"}
	prd := store.ApprovalPolicy{ID: "pol_prd", Reason: "Production VMs need sign-off", Condition: `environment == "prd"`}
	strict := leads
	strict.Mode = "all"
	chain := addLevel(addLevel(nil, big, leads), prd, strict)
	if len(chain) != 1 || len(chain[0].Reasons) != 2 || len(chain[0].PolicyIDs) != 2 || chain[0].Mode != "all" {
		t.Fatalf("merged chain = %+v", chain)
	}
	other := level{Name: "Network security", Approvers: []string{"usr_afavre"}}
	if chain = addLevel(chain, prd, other); len(chain) != 2 {
		t.Fatalf("different approvers should be another level: %+v", chain)
	}
}

func TestAddLevelNeverWeakensTimeouts(t *testing.T) {
	pol := store.ApprovalPolicy{ID: "p", Reason: "r"}
	lv := func(timeout, onTimeout string) level {
		return level{Name: "Leads", Approvers: []string{"usr_mkeller"}, Timeout: timeout, OnTimeout: onTimeout}
	}
	cases := []struct {
		name      string
		a, b      level
		timeout   time.Duration
		onTimeout string
	}{
		{"shorter timeout, stricter action", lv("24h", "approve"), lv("72h", "reject"), 24 * time.Hour, "reject"},
		{"escalate beats approve", lv("48h", "escalate"), lv("12h", "approve"), 12 * time.Hour, "escalate"},
		{"no timeout drops an auto-approve", lv("", ""), lv("24h", "approve"), 0, ""},
		{"no timeout keeps a reject", lv("", ""), lv("24h", "reject"), 24 * time.Hour, "reject"},
		{"order does not matter", lv("24h", "approve"), lv("", ""), 0, ""},
	}
	for _, c := range cases {
		chain := addLevel(addLevel(nil, pol, c.a), pol, c.b)
		if len(chain) != 1 || chain[0].Timeout != c.timeout || chain[0].OnTimeout != c.onTimeout {
			t.Errorf("%s: got %s %q, want %s %q", c.name, chain[0].Timeout, chain[0].OnTimeout, c.timeout, c.onTimeout)
		}
	}
}

func TestAllApprovedCountsDelegatesAndSkipsTheRequester(t *testing.T) {
	lv := store.Approval{ApproverIDs: []string{"usr_afavre", "usr_cdubois", "usr_tnguyen"}}
	votes := []store.ApprovalVote{
		{UserID: "usr_sbrunner", UserName: "Sophie Brunner", Decision: "approve"},
		{UserID: "usr_cdubois", UserName: "Claire Dubois", Decision: "approve"},
	}
	dels := []store.ApprovalDelegate{{UserID: "usr_sbrunner", DelegatedBy: "usr_afavre"}}
	names, done := allApproved(lv, "usr_tnguyen", votes, dels)
	if !done || len(names) != 2 {
		t.Fatalf("allApproved = %v %v", names, done)
	}
	if _, done := allApproved(lv, "usr_tnguyen", votes[1:], dels); done {
		t.Fatal("Alain Favre has not approved, himself or through a delegate")
	}
	if _, done := allApproved(store.Approval{}, "usr_tnguyen", nil, nil); !done {
		t.Fatal("a level with no named approvers needs only one approval")
	}
}
