package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/buildinfo"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/store"
)

// TestSystemPage: the System page (U1.5, ADR-0030) shows the version, the
// schema, each process and the revision it runs, the settings saved in
// Administration and how far each has applied, and the install settings
// as the API reads them, a secret's value never sent.
func TestSystemPage(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		h.json("GET", "/api/v1/admin/system", "lmartin", nil, 403, nil)

		t.Setenv("RVAV_SECRETS_KEY", "set-in-the-environment")
		t.Setenv("RVAV_TEMPORAL_ADDRESS", "")
		h.svc.Config.DatabaseURL = "postgres://rvav:s3cret-db@db.corp.example:5432/rvav?sslmode=require"
		worker := store.Process{ID: "rvav-worker-1/worker/7-a1b2c3", Role: "worker", Host: "rvav-worker-1", Version: "1.4.0", State: "running"}
		if err := h.store.SeeProcess(ctx, worker, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		// Email applies on next use; vCenter once the worker has started again.
		h.json("PUT", "/api/v1/admin/integrations/smtp", admin, api.IntegrationWrite{Settings: map[string]any{
			"host": "smtp.corp.example"}}, 200, nil)
		var vc api.Integration
		h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, api.IntegrationWrite{Settings: map[string]any{
			"url": c.URL().String(), "username": "user", "insecure": true, "name": "vc01.corp.example"}, Secret: new("pass")}, 200, &vc)

		system := func() (api.System, []byte) {
			t.Helper()
			var raw json.RawMessage
			h.json("GET", "/api/v1/admin/system", admin, nil, 200, &raw)
			var sys api.System
			if err := json.Unmarshal(raw, &sys); err != nil {
				t.Fatal(err)
			}
			return sys, raw
		}
		sys, raw := system()
		if sys.Version != buildinfo.Version() || sys.Schema < 48 || sys.Revision != vc.Revision {
			t.Fatalf("system = %s %d %d", sys.Version, sys.Schema, sys.Revision)
		}
		if len(sys.Processes) != 1 || sys.Processes[0].Id != worker.ID || sys.Processes[0].Version != "1.4.0" || sys.Processes[0].Revision != 0 {
			t.Fatalf("processes = %+v", sys.Processes)
		}
		if len(sys.Settings) != 2 || sys.Settings[0].Kind != api.IntegrationKindVcenter || sys.Settings[0].Applies != api.Applying ||
			sys.Settings[0].Revision != vc.Revision || sys.Settings[0].UpdatedBy == nil || sys.Settings[0].UpdatedBy.Name != "Marc Keller" ||
			sys.Settings[1].Kind != api.IntegrationKindSmtp || sys.Settings[1].Applies != api.NextUse {
			t.Fatalf("settings = %+v", sys.Settings)
		}

		// The install settings, in the reference's order: a secret's value
		// is never sent, the database URL without its password.
		var names []string
		shown := map[string]api.InstallSetting{}
		for _, s := range sys.Install {
			names = append(names, s.Name)
			shown[s.Name] = s
		}
		want := []string{}
		for _, s := range config.Install {
			want = append(want, s.Env)
		}
		if !slices.Equal(names, want) {
			t.Fatalf("install settings = %v", names)
		}
		if db := shown["RVAV_DATABASE_URL"]; !db.Secret || db.Value == nil ||
			*db.Value != "postgres://rvav:xxxxx@db.corp.example:5432/rvav?sslmode=require" {
			t.Fatalf("database URL = %+v", db)
		}
		if key := shown["RVAV_SECRETS_KEY"]; !key.Secret || key.Value != nil || !key.Set {
			t.Fatalf("secrets key = %+v", key)
		}
		if tmp := shown["RVAV_TEMPORAL_ADDRESS"]; tmp.Secret || tmp.Value == nil || *tmp.Value != h.svc.Config.Temporal.Address || tmp.Set {
			t.Fatalf("Temporal's address = %+v", tmp)
		}
		for _, secret := range []string{"s3cret-db", h.svc.Config.SecretsKey} {
			if secret != "" && bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("the System page sent a secret: %s", raw)
			}
		}

		// Exited to apply them, and not started again yet (Compose's restart,
		// Kubernetes' back-off): not applied, the worker shown restarting,
		// past the 30 s a process that stopped recording itself is listed.
		if err := h.store.ProcessRestarting(ctx, worker.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `update processes set seen_at = now() - interval '1 minute' where id = $1`, worker.ID); err != nil {
			t.Fatal(err)
		}
		if sys, _ = system(); sys.Settings[0].Applies != api.Applying || len(sys.Processes) != 1 ||
			sys.Processes[0].State != api.ProcessStateRestarting {
			t.Fatalf("restarting = %+v %+v", sys.Settings[0], sys.Processes)
		}
		var vcNow api.IntegrationList
		h.json("GET", "/api/v1/admin/integrations", admin, nil, 200, &vcNow)
		for _, it := range vcNow.Items {
			if it.Kind == "vcenter" && !it.Applying {
				t.Fatal("vCenter applied with the worker restarting")
			}
		}

		// Started again with them (its successor on the same host), the
		// worker has applied vCenter's, and the one restarting is forgotten.
		worker.ID, worker.Revision = "rvav-worker-1/worker/8-d4e5f6", vc.Revision
		if err := h.store.SeeProcess(ctx, worker, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if sys, _ = system(); sys.Settings[0].Applies != api.Applied || len(sys.Processes) != 1 || sys.Processes[0].Revision != vc.Revision {
			t.Fatalf("applied = %+v %+v", sys.Settings[0], sys.Processes)
		}
	})
}
