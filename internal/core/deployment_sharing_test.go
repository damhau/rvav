package core

import (
	"testing"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

func TestSeesDeployment(t *testing.T) {
	as := func(id string, assignments ...auth.Assignment) *auth.Principal {
		return &auth.Principal{User: store.User{ID: id}, Grants: auth.Compute(assignments, auth.BuiltinRole)}
	}
	member := as("usr_m", auth.Assignment{Role: "member", ProjectID: "prj_a"})
	cases := []struct {
		name    string
		p       *auth.Principal
		sharing string
		owner   string
		want    bool
	}{
		{"a member, shared with all members", member, store.SharingAllMembers, "usr_o", true},
		{"a member, owners only", member, store.SharingOwnerOnly, "usr_o", false},
		{"its owner", member, store.SharingOwnerOnly, "usr_m", true},
		{"its owner outside the project", as("usr_o"), store.SharingOwnerOnly, "usr_o", true},
		{"a project admin", as("usr_p", auth.Assignment{Role: "project-admin", ProjectID: "prj_a"}), store.SharingOwnerOnly, "usr_o", true},
		{"a supervisor", as("usr_s", auth.Assignment{Role: "supervisor", ProjectID: "prj_a"}), store.SharingOwnerOnly, "usr_o", true},
		{"an auditor", as("usr_a", auth.Assignment{Role: "auditor"}), store.SharingOwnerOnly, "usr_o", true},
		{"a project admin elsewhere", as("usr_p", auth.Assignment{Role: "project-admin", ProjectID: "prj_b"}), store.SharingAllMembers, "usr_o", false},
	}
	for _, c := range cases {
		if got := SeesDeployment(c.p, "prj_a", c.sharing, c.owner); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
