package inventory

import "testing"

// A watcher's reads cover its vCenter's VMs, and the first's those of names
// rvav doesn't know, as rvav manages vCenters now, not when it started.
func TestOwnsFollowsTheVCenters(t *testing.T) {
	s := &Syncer{names: []string{"vc01"}}
	first, second := s.ownsFor("vc01"), s.ownsFor("vc02")
	if !first("vc01") || !first("vc02") || !first("") || second("vc01") {
		t.Fatal("before vc02 is added")
	}
	s.mu.Lock()
	s.names = []string{"vc01", "vc02"}
	s.mu.Unlock()
	if !first("vc01") || first("vc02") || !first("renamed") || !second("vc02") {
		t.Fatal("after vc02 is added, the first still takes its VMs")
	}
}
