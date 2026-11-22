package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/nsx"
)

// TestNSXManagers: an administrator adds a second NSX Manager, its
// password sealed, tests it, and links a vCenter to it, which leaves the
// NSX integration's; the integration's is listed first and changed only
// in Integrations (I1.1, ADR-0062).
func TestNSXManagers(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		const nsx02 = "nsx02.corp.example"
		h.svc.Config.VCenter.Name, h.svc.Config.VCenter.URL = "vc01.corp.example", "https://vc01.corp.example/sdk"
		useFakeNSX(h, nsx.NewFakeServer())
		h.json("POST", "/api/v1/admin/vcenters", admin, api.VCenterWrite{Name: "vc02.corp.example", Url: "https://vc02.corp.example/sdk",
			Username: "rvav", Password: new("vc02-password-1")}, 201, nil)

		// Today's NSX Manager serves the vCenter integration's vCenter; one
		// added since serves none until linked.
		var list api.NsxManagerList
		h.json("GET", "/api/v1/admin/nsx-managers", admin, nil, 200, &list)
		if len(list.Items) != 1 || !list.Items[0].Primary || list.Items[0].Name != "127.0.0.1" ||
			!slices.Equal(list.Items[0].Vcenters, []string{"vc01.corp.example"}) {
			t.Fatalf("NSX Managers = %+v", list.Items)
		}

		second := httptest.NewTLSServer(nsx.NewFakeServer())
		defer second.Close()
		var problem struct{ Errors []api.FieldError }
		write := api.NsxManagerWrite{Name: nsx02, Url: "http://nsx02.corp.example", Username: new(nsx.FakeUser),
			Vcenters: &[]string{"vc03.corp.example"}}
		h.json("POST", "/api/v1/admin/nsx-managers", admin, write, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/url", "/password", "/vcenters/0"}) {
			t.Fatalf("refused for %v: %+v", got, problem.Errors)
		}
		write.Url, write.Password, write.Insecure, write.Vcenters = second.URL, new(nsx.FakePassword), new(true), nil
		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/nsx-managers/"+nsx02+"/test", admin, write, 200, &test)
		if !test.Ok || len(test.Checks) != 5 || test.Checks[1].Message != "Signed in to NSX 4.1.2.0.0.22589037." {
			t.Fatalf("test = %+v", test.Checks)
		}
		var added api.NsxManager
		h.json("POST", "/api/v1/admin/nsx-managers", admin, write, 201, &added)
		if !added.PasswordSet || len(added.Vcenters) != 0 || added.Primary {
			t.Fatalf("added = %+v", added)
		}
		h.json("POST", "/api/v1/admin/nsx-managers", admin, write, 409, nil)
		h.json("POST", "/api/v1/admin/nsx-managers/"+nsx02+"/test", admin, nil, 200, &test)
		if !test.Ok {
			t.Fatalf("saved settings' test = %+v", test.Checks)
		}
		// Its address changed without the password: refused.
		moved := write
		moved.Url, moved.Password = "https://elsewhere.example", nil
		h.json("PUT", "/api/v1/admin/nsx-managers/"+nsx02, admin, moved, 422, &problem)

		// vc02 moves to it from the integration's; vc01 stays.
		var linked api.NsxManager
		h.json("PUT", "/api/v1/admin/nsx-managers/"+nsx02+"/vcenters", admin, api.NsxManagerVCenters{Vcenters: []string{"vc02.corp.example"}},
			200, &linked)
		h.json("PUT", "/api/v1/admin/nsx-managers/127.0.0.1/vcenters", admin,
			api.NsxManagerVCenters{Vcenters: []string{"vc01.corp.example", "vc02.corp.example"}}, 200, nil)
		h.json("PUT", "/api/v1/admin/nsx-managers/"+nsx02+"/vcenters", admin, api.NsxManagerVCenters{Vcenters: []string{"vc02.corp.example"}},
			200, &linked)
		h.json("GET", "/api/v1/admin/nsx-managers", admin, nil, 200, &list)
		if len(list.Items) != 2 || !slices.Equal(list.Items[0].Vcenters, []string{"vc01.corp.example"}) || list.Items[1].Name != nsx02 ||
			!slices.Equal(list.Items[1].Vcenters, []string{"vc02.corp.example"}) || !slices.Equal(linked.Vcenters, list.Items[1].Vcenters) {
			t.Fatalf("after linking = %+v", list.Items)
		}

		// The integration's is changed in Integrations; the auditor reads.
		h.json("PUT", "/api/v1/admin/nsx-managers/127.0.0.1", admin, write, 409, nil)
		h.json("DELETE", "/api/v1/admin/nsx-managers/127.0.0.1", admin, nil, 409, nil)
		h.json("GET", "/api/v1/admin/nsx-managers", "svc-awx", nil, 200, nil)
		h.json("POST", "/api/v1/admin/nsx-managers", "svc-awx", write, 403, nil)

		// A vCenter removed leaves its NSX Manager; a Manager removed, its
		// vCenters.
		h.json("DELETE", "/api/v1/admin/vcenters/vc02.corp.example", admin, nil, 204, nil)
		h.json("GET", "/api/v1/admin/nsx-managers", admin, nil, 200, &list)
		if len(list.Items[1].Vcenters) != 0 {
			t.Fatalf("vc02 gone = %+v", list.Items[1])
		}
		h.json("DELETE", "/api/v1/admin/nsx-managers/"+nsx02, admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/nsx-managers/"+nsx02, admin, nil, 404, nil)
	})
}

// TestNSXManagerCertificateDropsPassword: an added NSX Manager switched to a
// principal identity forgets its password, so switching back at another
// address needs it again: the saved one never reaches a server it wasn't
// given for, even in two saves (I1.6). The integration's NSX Manager keeps
// refusing a move without the password, whatever its sign-in.
func TestNSXManagerCertificateDropsPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, nsx02 = "mkeller", "nsx02.corp.example"
		useFakeNSX(h, nsx.NewFakeServer())
		real := httptest.NewTLSServer(nsx.NewFakeServer())
		defer real.Close()
		var seen atomic.Value
		trap := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, p, ok := r.BasicAuth(); ok {
				seen.Store(p)
			}
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer trap.Close()
		base := "/api/v1/admin/nsx-managers/" + nsx02
		h.json("POST", "/api/v1/admin/nsx-managers", admin, api.NsxManagerWrite{Name: nsx02, Url: real.URL, Username: new(nsx.FakeUser),
			Password: new(nsx.FakePassword), Insecure: new(true)}, 201, nil)

		var saved api.NsxManager
		h.json("PUT", base, admin, api.NsxManagerWrite{Name: nsx02, Url: trap.URL, CertFile: new("/nonexistent/client.crt"),
			KeyFile: new("/nonexistent/client.key"), Insecure: new(true), Password: new(nsx.FakePassword)}, 200, &saved)
		if saved.PasswordSet {
			t.Fatalf("a principal identity keeps a password: %+v", saved)
		}
		var problem struct{ Errors []api.FieldError }
		back := api.NsxManagerWrite{Name: nsx02, Url: trap.URL, Username: new(nsx.FakeUser), Insecure: new(true)}
		h.json("PUT", base, admin, back, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/password"}) {
			t.Fatalf("the account again without its password = %+v", problem.Errors)
		}
		h.json("POST", base+"/test", admin, back, 422, nil)
		var test api.IntegrationTest
		h.json("POST", base+"/test", admin, nil, 200, &test)
		if p, _ := seen.Load().(string); p != "" {
			t.Fatalf("a password went to %s, which it wasn't given for", trap.URL)
		}
		back.Url, back.Password = real.URL, new(nsx.FakePassword)
		h.json("PUT", base, admin, back, 200, &saved)
		if !saved.PasswordSet {
			t.Fatalf("the account again = %+v", saved)
		}

		h.json("PUT", "/api/v1/admin/integrations/nsx", admin, api.IntegrationWrite{Settings: map[string]any{"url": trap.URL,
			"cert_file": "/nonexistent/client.crt", "key_file": "/nonexistent/client.key"}}, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/secret"}) {
			t.Fatalf("the integration's moved with a certificate = %+v", problem.Errors)
		}
	})
}
