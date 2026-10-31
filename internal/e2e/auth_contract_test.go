package e2e

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
)

// TestAuthContract: what the sign-in endpoints answer is what the spec says
// they do, so the generated client's types hold: each status sent is one
// the operation lists, sign-in lists none it never sends, and the options'
// headers are declared.
func TestAuthContract(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	listed := func(method, path string) []int {
		t.Helper()
		item := spec.Paths.Find("/api/v1" + path)
		if item == nil {
			item = spec.Paths.Find(path)
		}
		if item == nil || item.GetOperation(method) == nil {
			t.Fatalf("no %s %s in the spec", method, path)
		}
		var out []int
		for code := range item.GetOperation(method).Responses.Map() {
			n, _ := strconv.Atoi(code)
			out = append(out, n)
		}
		slices.Sort(out)
		return out
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const password = "the contract's own password"
		if _, err := core.ResetLocalAdmin(ctx, h.store, password); err != nil {
			t.Fatal(err)
		}
		b := newBrowser(t, ctx, h.srv.URL)
		sent := map[string][]int{}
		send := func(method, path string, body any, bare bool, want int) http.Header {
			t.Helper()
			code, out, hdr := b.do(method, "/api/v1"+path, body, bare)
			if code != want {
				t.Fatalf("%s %s = %d, want %d: %s", method, path, code, want, out)
			}
			if !slices.Contains(listed(method, path), code) {
				t.Errorf("%s %s sent %d, which the spec doesn't list (%v)", method, path, code, listed(method, path))
			}
			sent[method+" "+path] = append(sent[method+" "+path], code)
			return hdr
		}

		send("POST", "/auth/sign-in", "not an object", false, http.StatusBadRequest)
		send("POST", "/auth/sign-in", api.SignInRequest{Username: strings.Repeat("a", 65), Password: "x"}, false, http.StatusUnauthorized)
		send("POST", "/auth/sign-in", api.SignInRequest{Username: "admin", Password: strings.Repeat("x", 257)}, false, http.StatusUnauthorized)
		send("POST", "/auth/sign-in", api.SignInRequest{Username: "admin", Password: password}, false, http.StatusOK)
		send("POST", "/auth/password", "not an object", false, http.StatusBadRequest)
		send("POST", "/auth/sign-out", nil, true, http.StatusForbidden)
		send("POST", "/auth/sign-out", nil, false, http.StatusNoContent)

		hdr := send("GET", "/auth/options", nil, false, http.StatusOK)
		declared := spec.Paths.Find("/auth/options").Get.Responses.Value("200").Value.Headers
		for _, name := range []string{"X-Rvav-Idp-Origins", "X-Rvav-Script-Origins"} {
			if _, ok := hdr[name]; !ok {
				t.Errorf("GET /auth/options doesn't send %s", name)
			}
			if declared[name] == nil {
				t.Errorf("the spec doesn't declare GET /auth/options' %s", name)
			}
		}

		// Sign-in lists what it sends: 429 is TestLocalAdmin's.
		got := append(slices.Clone(sent["POST /auth/sign-in"]), http.StatusTooManyRequests)
		slices.Sort(got)
		got = slices.Compact(got)
		if want := listed("POST", "/auth/sign-in"); !slices.Equal(got, want) {
			t.Errorf("sign-in sends %v; the spec lists %v", got, want)
		}
	})
}
