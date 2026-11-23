package core

import (
	"testing"

	"github.com/damhau/rvav/internal/store"
)

// TestProjectPlacementNamesTheGroup: a group a project-only role may not
// add is named, so that the one refused among several is found.
func TestProjectPlacementNamesTheGroup(t *testing.T) {
	const base = "DC=corp,DC=example"
	delegated := &store.ADPlacement{OU: "OU=Servers", Groups: []string{"CN=All servers,OU=Servers"}}
	errs := projectPlacement(delegated, nil, "OU=Web,OU=Servers", []string{"CN=Web,OU=Web,OU=Servers", "CN=Domain Admins,CN=Users"}, base)
	want := "Your role manages this project's placement only: CN=Domain Admins,CN=Users,DC=corp,DC=example is not under the project's OU, " +
		"nor one of the groups set for every project: CN=All servers,OU=Servers."
	if len(errs) != 1 || errs[0].Field != "/groups/1" || errs[0].Message != want {
		t.Fatalf("errors = %+v", errs)
	}
	delegated.Groups = nil
	errs = projectPlacement(delegated, nil, "OU=Web,OU=Servers", []string{"CN=Domain Admins,CN=Users"}, base)
	if len(errs) != 1 || errs[0].Message != "Your role manages this project's placement only: CN=Domain Admins,CN=Users,DC=corp,DC=example is not under the project's OU." {
		t.Fatalf("no group set for every project = %+v", errs)
	}
}
