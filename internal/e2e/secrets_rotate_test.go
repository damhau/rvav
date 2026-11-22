package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auditlog"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/secretinput"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vcenters"
)

// TestSecretsRotateEverySealedColumn: rvav secrets rotate seals again
// every value sealed with an old key, in every table that keeps one (I1.6).
// The test finds the sealed columns in the schema: a table added with one
// fails it until the test seeds a value there and rotation reseals it.
func TestSecretsRotateEverySealedColumn(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		old := h.svc.Secrets
		useFakeNSX(h, nsx.NewFakeServer())
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb", Kind: api.SecretWriteKindToken,
			Value: new("cmdb-token-for-rotation")}, 201, nil)
		h.json("PUT", "/api/v1/admin/integrations/awx", admin, api.IntegrationWrite{Settings: map[string]any{
			"url": "https://awx.corp.example", "organization": "rvav-test"}, Secret: new("awx-token-for-rotation")}, 200, nil)
		h.json("POST", "/api/v1/admin/vcenters", admin, api.VCenterWrite{Name: "vc02.corp.example", Url: "https://vc02.corp.example/sdk",
			Username: "rvav", Password: new("vc02-password-for-rotation")}, 201, nil)
		h.json("POST", "/api/v1/admin/nsx-managers", admin, api.NsxManagerWrite{Name: "nsx02.corp.example", Url: "https://nsx02.corp.example",
			Username: new(nsx.FakeUser), Password: new("nsx02-password-for-rotation")}, 201, nil)
		r, dep := requestBuild(h, "tnguyen")
		seal := func(name, value string) (string, []byte, []byte) {
			k, n, ct, err := old.Seal(name, []byte(value))
			if err != nil {
				t.Fatal(err)
			}
			return k, n, ct
		}
		k, n, ct := seal(secretinput.Name(r.Id, "password"), "request-password-for-rotation")
		if err := h.store.PutRequestSecret(ctx, h.store.Pool, r.Id, store.SealedInput{Field: "password", KeyID: k, Nonce: n,
			Ciphertext: ct}); err != nil {
			t.Fatal(err)
		}
		k, n, ct = seal(store.DeploymentKeyName(dep), "private-key-for-rotation")
		if _, err := h.store.KeepDeploymentKey(ctx, store.DeploymentKey{DeploymentID: dep, Username: "rvav", PublicKey: "ssh-ed25519 AAAA",
			Fingerprint: "SHA256:x", KeyID: &k, Nonce: n, Ciphertext: ct}); err != nil {
			t.Fatal(err)
		}
		dest := ids.New(ids.AuditDestination)
		k, n, ct = seal(auditlog.AuthSecretName(dest), "Bearer header-for-rotation")
		settings, _ := json.Marshal(map[string]any{"url": "https://siem.corp.example/hook", "auth_header": "Authorization"})
		if err := h.store.SaveAuditDestination(ctx, store.AuditDestination{ID: dest, Name: "siem", Kind: "webhook", Settings: settings,
			AuthKeyID: k, AuthNonce: n, AuthCiphertext: ct}); err != nil {
			t.Fatal(err)
		}

		newKey := base64.StdEncoding.EncodeToString([]byte("another-e2e-secrets-key-32-bytes"))
		both, err := secrets.New(newKey, base64.StdEncoding.EncodeToString([]byte("e2e-only-secrets-key-32-bytes!!!")))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := core.RotateSecrets(ctx, h.store, both); err != nil {
			t.Fatal(err)
		}
		rows, _ := h.store.Pool.Query(ctx, `select table_name::text, column_name::text from information_schema.columns
			where table_schema = current_schema() and column_name like '%ciphertext' order by 1, 2`)
		type column struct{ Table, Column string }
		cols, err := pgx.CollectRows(rows, pgx.RowToStructByPos[column])
		if err != nil || len(cols) < 7 {
			t.Fatalf("sealed columns = %v %v", cols, err)
		}
		for _, col := range cols {
			keyCol := strings.Replace(col.Column, "ciphertext", "key_id", 1)
			var sealed, stale int
			if err := h.store.Pool.QueryRow(ctx, `select count(*), count(*) filter (where coalesce(`+keyCol+`, '') <> $1) from `+
				col.Table+` where `+col.Column+` is not null`, both.KeyID()).Scan(&sealed, &stale); err != nil {
				t.Fatalf("%s.%s: %v", col.Table, col.Column, err)
			}
			switch {
			case sealed == 0:
				t.Errorf("%s.%s keeps sealed values, and this test seeds none: seed one, and see that rvav secrets rotate reseals it",
					col.Table, col.Column)
			case stale > 0:
				t.Errorf("%s.%s: %d of %d still sealed with the old key after rvav secrets rotate", col.Table, col.Column, stale, sealed)
			}
		}
		onlyNew, _ := secrets.New(newKey)
		vc, err := h.store.GetVCenter(ctx, "vc02.corp.example")
		if err != nil {
			t.Fatal(err)
		}
		if plain, err := onlyNew.Open(vcenters.SecretName(vc.Name), vc.KeyID, vc.Nonce, vc.Ciphertext); err != nil ||
			string(plain) != "vc02-password-for-rotation" {
			t.Fatalf("vc02's password with the new key alone: %q %v", plain, err)
		}
		if n, err := core.RotateSecrets(ctx, h.store, both); err != nil || n != 0 {
			t.Fatalf("rotated %d again, %v", n, err)
		}
	})
}
