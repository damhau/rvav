package servicenow

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/trust"
)

func serve(t *testing.T) (*Fake, *httptest.Server, trust.Options) {
	t.Helper()
	f := NewFake()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return f, srv, trust.Options{Roots: pool}
}

func TestClientSignsIn(t *testing.T) {
	f, srv, tlsOpts := serve(t)
	ctx := context.Background()
	f.Insert(TableRequestedItem, Record{"number": "RITM0010001"})
	for _, c := range []Config{
		{URL: srv.URL, Auth: AuthBasic, Username: f.Username, Secret: f.Password, TLS: tlsOpts},
		{URL: srv.URL, Auth: AuthOAuth, ClientID: f.ClientID, Secret: f.ClientSecret, TLS: tlsOpts},
	} {
		cl, err := New(c)
		if err != nil {
			t.Fatal(err)
		}
		recs, err := cl.Records(ctx, TableRequestedItem, Query{Limit: 1, Fields: []string{"number"}})
		if err != nil || len(recs) != 1 || recs[0]["number"] != "RITM0010001" {
			t.Fatalf("%s: %v %v", c.Auth, recs, err)
		}
	}
	// A token ServiceNow forgot is asked for again, once.
	cl, _ := New(Config{URL: srv.URL, Auth: AuthOAuth, ClientID: f.ClientID, Secret: f.ClientSecret, TLS: tlsOpts})
	if _, err := cl.Records(ctx, TableRequestedItem, Query{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	clear(f.tokens)
	f.mu.Unlock()
	if _, err := cl.Records(ctx, TableRequestedItem, Query{Limit: 1}); err != nil {
		t.Fatalf("after the token was forgotten: %v", err)
	}

	bad, _ := New(Config{URL: srv.URL, Auth: AuthBasic, Username: f.Username, Secret: "wrong", TLS: tlsOpts})
	_, err := bad.Records(ctx, TableRequestedItem, Query{Limit: 1})
	if e, ok := err.(*APIError); !ok || e.Status != http.StatusUnauthorized || !strings.Contains(e.Message, "User Not Authenticated") {
		t.Fatalf("wrong password: %v", err)
	}
	badClient, _ := New(Config{URL: srv.URL, Auth: AuthOAuth, ClientID: f.ClientID, Secret: "wrong", TLS: tlsOpts})
	if _, err := badClient.Records(ctx, TableRequestedItem, Query{Limit: 1}); err == nil || !strings.Contains(err.Error(), "OAuth client was refused") {
		t.Fatalf("wrong client secret: %v", err)
	}
	// Its certificate is checked.
	untrusted, _ := New(Config{URL: srv.URL, Auth: AuthBasic, Username: f.Username, Secret: f.Password})
	if _, err := untrusted.Records(ctx, TableRequestedItem, Query{Limit: 1}); err == nil {
		t.Fatal("a certificate no CA vouches for was accepted")
	}
	pinned, _ := New(Config{URL: srv.URL, Auth: AuthBasic, Username: f.Username, Secret: f.Password,
		TLS: trust.Options{Pin: trust.Fingerprint(srv.Certificate())}})
	if _, err := pinned.Records(ctx, TableRequestedItem, Query{Limit: 1}); err != nil {
		t.Fatalf("pinned: %v", err)
	}
}

func TestNewRefuses(t *testing.T) {
	for _, c := range []Config{
		{URL: "http://snow.example", Auth: AuthBasic, Username: "u", Secret: "p"},
		{URL: "snow.example", Auth: AuthBasic, Username: "u", Secret: "p"},
		{URL: "https://snow.example", Auth: AuthBasic, Username: "u"},
		{URL: "https://snow.example", Auth: AuthOAuth, Secret: "p"},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("%+v was taken", c)
		}
	}
}

func TestRecordsAndUpdates(t *testing.T) {
	f, srv, tlsOpts := serve(t)
	ctx := context.Background()
	cl, _ := New(Config{URL: srv.URL, Auth: AuthBasic, Username: f.Username, Secret: f.Password, TLS: tlsOpts})
	for i := range 7 {
		src := "rvav"
		if i == 6 {
			src = "ServiceNow"
		}
		f.Insert(DefaultClass, Record{"name": "vm" + string(rune('a'+i)), "discovery_source": src, "install_status": "1"})
	}
	all, err := cl.All(ctx, DefaultClass, Query{Encoded: "discovery_source=rvav^install_status!=7", Fields: []string{"sys_id", "name"}}, 4)
	if err != nil || len(all) != 6 {
		t.Fatalf("%d records, %v", len(all), err)
	}
	if err := cl.Update(ctx, DefaultClass, all[0]["sys_id"], map[string]string{"install_status": InstallRetired}); err != nil {
		t.Fatal(err)
	}
	left, _ := cl.All(ctx, DefaultClass, Query{Encoded: "discovery_source=rvav^install_status!=7"}, 100)
	if len(left) != 5 {
		t.Fatalf("%d left after one was retired", len(left))
	}
	if err := cl.Update(ctx, DefaultClass, "nope", map[string]string{"name": "x"}); !IsNotFound(err) {
		t.Fatalf("a record that isn't there: %v", err)
	}

	ritm := f.Insert(TableRequestedItem, Record{"number": "RITM0010002", "state": "1"})
	if err := cl.Update(ctx, TableRequestedItem, ritm, map[string]string{"state": ItemWorkInProgress, "work_notes": "rvav: building"}); err != nil {
		t.Fatal(err)
	}
	if r := f.Row(TableRequestedItem, ritm); r["state"] != ItemWorkInProgress || r["work_notes"] != "" {
		t.Fatalf("%v", r)
	}
	if n := f.WorkNotes(ritm); len(n) != 1 || n[0] != "rvav: building" {
		t.Fatalf("work notes %v", n)
	}

	f.Insert(TableUser, Record{"email": "tnguyen@corp.example", "active": "true"})
	f.Insert(TableUser, Record{"email": "gone@corp.example", "active": "false"})
	if id, err := cl.UserSysID(ctx, "tnguyen@corp.example"); err != nil || id == "" {
		t.Fatalf("%q %v", id, err)
	}
	if id, _ := cl.UserSysID(ctx, "gone@corp.example"); id != "" {
		t.Fatal("an inactive user was found")
	}
	if id, _ := cl.UserSysID(ctx, "x@corp.example^active=false"); id != "" {
		t.Fatal("a query was injected")
	}
}

func TestIdentify(t *testing.T) {
	f, srv, tlsOpts := serve(t)
	ctx := context.Background()
	cl, _ := New(Config{URL: srv.URL, Auth: AuthOAuth, ClientID: f.ClientID, Secret: f.ClientSecret, TLS: tlsOpts})
	ci := CI{Class: DefaultClass, NativeKey: "4215-aa", Values: map[string]string{
		"name": "web01", "vm_instance_uuid": "4215-aa", "object_id": "vm-101", "vcenter_uuid": "vc-1", "cpus": "2"}}
	first, err := cl.Identify(ctx, "rvav", ci)
	if err != nil || first.Operation != "INSERT" {
		t.Fatalf("%+v %v", first, err)
	}
	if r := f.Row(DefaultClass, first.SysID); r["discovery_source"] != "rvav" || r["cpus"] != "2" {
		t.Fatalf("%v", r)
	}
	ci.Values["cpus"] = "4"
	again, err := cl.Identify(ctx, "rvav", ci)
	if err != nil || again.SysID != first.SysID || again.Operation != "UPDATE" {
		t.Fatalf("%+v %v", again, err)
	}
	same, _ := cl.Identify(ctx, "rvav", ci)
	if same.Operation != "NO_CHANGE" {
		t.Fatalf("%+v", same)
	}
	// Discovered by vCenter before rvav knew its instance UUID: its
	// reference and vCenter find it.
	found := f.Insert(DefaultClass, Record{"name": "db01", "object_id": "vm-202", "vcenter_uuid": "vc-1", "discovery_source": "ServiceNow"})
	got, err := cl.Identify(ctx, "rvav", CI{Class: DefaultClass, NativeKey: "4215-bb", Values: map[string]string{
		"name": "db01", "vm_instance_uuid": "4215-bb", "object_id": "vm-202", "vcenter_uuid": "vc-1"}})
	if err != nil || got.SysID != found {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := cl.Identify(ctx, "elsewhere", ci); err == nil || !strings.Contains(err.Error(), "Invalid data source") {
		t.Fatalf("an unknown data source: %v", err)
	}
	if _, err := cl.Identify(ctx, "rvav", CI{Class: DefaultClass, Values: map[string]string{"name": "x"}}); err == nil ||
		!strings.Contains(err.Error(), "MISSING_MATCHING_ATTRIBUTES") {
		t.Fatalf("nothing to match on: %v", err)
	}
	f.FailOn("/api/now/identifyreconcile", http.StatusServiceUnavailable)
	_, err = cl.Identify(ctx, "rvav", ci)
	if e, ok := err.(*APIError); !ok || !e.Temporary() {
		t.Fatalf("a 503: %v", err)
	}
}

func TestIsRefused(t *testing.T) {
	for status, want := range map[int]bool{400: true, 403: true, 409: true, 422: true,
		401: false, 404: false, 408: false, 429: false, 500: false, 503: false} {
		if got := IsRefused(&APIError{Method: "PATCH", Path: "/api/now/table/sc_req_item/x", Status: status}); got != want {
			t.Errorf("%d: IsRefused = %v", status, got)
		}
	}
	if IsRefused(context.DeadlineExceeded) {
		t.Error("a timeout is no refusal")
	}
}
