package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestConsolePasswordFromTheEnvironment: with the console's password set
// by RVAV_CONSOLE_VCENTER_PASSWORD, vCenter can't be moved in
// Administration (another address, TLS checks off, a pin): the password
// would go to it. Its other settings still save.
func TestConsolePasswordFromTheEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		for _, k := range []string{"RVAV_VCENTER_URL", "RVAV_VCENTER_PASSWORD", "RVAV_VCENTER_INSECURE"} {
			t.Setenv(k, "")
		}
		t.Setenv("RVAV_CONSOLE_VCENTER_PASSWORD", "from-the-env")
		write := func(settings map[string]any) api.IntegrationWrite {
			return api.IntegrationWrite{Settings: settings, Secret: new("pass")}
		}
		for name, settings := range map[string]map[string]any{
			"another address": {"url": "https://vc02.corp.example/sdk", "username": "user"},
			"TLS checks off":  {"url": h.svc.Config.VCenter.URL, "username": "user", "insecure": true},
			"a pin":           {"url": h.svc.Config.VCenter.URL, "username": "user", "cert_sha256": strings.Repeat("AB:", 31) + "AB"},
		} {
			code, out, _ := h.do("PUT", "/api/v1/admin/integrations/vcenter", admin, write(settings))
			if code != http.StatusUnprocessableEntity || !strings.Contains(string(out), "locked_secret") ||
				!strings.Contains(string(out), "RVAV_CONSOLE_VCENTER_PASSWORD") {
				t.Errorf("%s with the console's password from the environment = %d %s", name, code, out)
			}
		}
		var it api.Integration
		h.json("PUT", "/api/v1/admin/integrations/vcenter", admin, write(map[string]any{"url": h.svc.Config.VCenter.URL,
			"username": "user", "insecure": h.svc.Config.VCenter.Insecure, "sync_interval": "10m"}), http.StatusOK, &it)
	})
}
