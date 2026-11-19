package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
)

// TestConcurrentAdminChanges changes project members and role assignments
// at once, as terraform apply does ten at a time (O1.2). Each takes the
// lock on who administers rvav: waiters holding a pooled connection each
// left the holder none once they were as many as the pool's connections.
func TestConcurrentAdminChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		client := &http.Client{Timeout: 30 * time.Second}
		call := func(method, path string, body any) (int, string) {
			b, _ := json.Marshal(body)
			req, _ := http.NewRequestWithContext(ctx, method, h.srv.URL+path, bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Rvav-Dev-User", "mkeller")
			resp, err := client.Do(req)
			if err != nil {
				return 0, err.Error()
			}
			defer resp.Body.Close()
			out, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(out)
		}
		var wg sync.WaitGroup
		for _, user := range []string{"usr_cdubois", "usr_tnguyen", "usr_mkeller", "usr_svc-awx"} {
			for _, project := range []string{"prj_web", "prj_core"} {
				wg.Go(func() {
					if code, body := call("PUT", "/api/v1/projects/"+project+"/members/"+user, map[string]any{"roles": []string{"viewer"}}); code != 200 {
						t.Errorf("member %s in %s = %d %s", user, project, code, body)
					}
				})
			}
			wg.Go(func() {
				if code, body := call("POST", "/api/v1/admin/role-assignments", map[string]any{"user_id": user, "role": "catalog-admin"}); code != 201 {
					t.Errorf("assignment of %s = %d %s", user, code, body)
				}
			})
		}
		wg.Wait()
	})
}
