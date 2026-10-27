package events

import (
	"encoding/json"
	"testing"

	"github.com/damhau/rvav/internal/store"
)

func TestFilterMatches(t *testing.T) {
	web, fin := "prj_web", "prj_fin"
	reqEvent := store.Event{ID: 1, ProjectID: &web, Topics: []string{"requests", "request:req_1"}}
	finEvent := store.Event{ID: 2, ProjectID: &fin, Topics: []string{"requests", "request:req_2"}}
	note := store.Event{ID: 3, Topics: []string{"notifications", "user:usr_lmartin"}}
	approval := store.Event{ID: 4, Topics: []string{"approvals", "user:usr_mkeller", "user:usr_cdubois"}}
	seesWeb := func(p string) bool { return p == web }

	cases := []struct {
		name   string
		filter Filter
		event  store.Event
		want   bool
	}{
		{"requests in project", Filter{Topics: []string{"requests"}, ProjectID: web, CanSeeProject: seesWeb}, reqEvent, true},
		{"other project filtered by project_id", Filter{Topics: []string{"requests"}, ProjectID: web, CanSeeProject: func(string) bool { return true }}, finEvent, false},
		{"other project not visible", Filter{Topics: []string{"requests"}, CanSeeProject: seesWeb}, finEvent, false},
		{"single request", Filter{Topics: []string{"request:req_1"}, CanSeeProject: seesWeb}, reqEvent, true},
		{"deployments topic does not get requests", Filter{Topics: []string{"deployments"}, CanSeeProject: seesWeb}, reqEvent, false},
		{"own notification", Filter{Topics: []string{"notifications"}, UserID: "usr_lmartin"}, note, true},
		{"someone else's notification", Filter{Topics: []string{"notifications"}, UserID: "usr_tnguyen"}, note, false},
		{"approval for me", Filter{Topics: []string{"approvals"}, UserID: "usr_mkeller"}, approval, true},
		{"approval for someone else", Filter{Topics: []string{"approvals"}, UserID: "usr_lmartin"}, approval, false},
		{"an approval is not a notification", Filter{Topics: []string{"notifications"}, UserID: "usr_mkeller"}, approval, false},
		{"a request checked readable passes outside the project (approver)",
			Filter{Topics: []string{"request:req_2"}, CanSeeProject: seesWeb, Requests: map[string]bool{"req_2": true}}, finEvent, true},
		{"but not the project's other requests",
			Filter{Topics: []string{"requests"}, CanSeeProject: seesWeb, Requests: map[string]bool{"req_9": true}}, finEvent, false},
	}
	for _, c := range cases {
		if got := c.filter.Matches(c.event); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEncodeAddsID(t *testing.T) {
	b, err := Encode(store.Event{ID: 42, Payload: json.RawMessage(`{"type":"request.updated","at":"2026-10-26T14:38:02Z","data":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["id"] != "42" || m["type"] != "request.updated" {
		t.Fatalf("encoded = %s", b)
	}
}
