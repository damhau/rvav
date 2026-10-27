package e2e

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/vsphere"
)

// fakeESXi accepts WebSocket upgrades on /ticket/good and echoes what it
// receives, as far as the proxy can tell an ESXi host's WebMKS endpoint.
func fakeESXi(t *testing.T) *httptest.Server {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ticket/good" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: binary\r\n\r\n",
			wsAccept(r.Header.Get("Sec-WebSocket-Key")))
		_ = buf.Flush()
		_, _ = io.Copy(conn, buf)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}

// consoleSource is vcsim (clone tickets) with WebMKS tickets of a fake host.
type consoleSource struct {
	*vsphere.Client
	mks vsphere.WebMKS
}

func (c consoleSource) WebMKSTicket(context.Context, string) (*vsphere.WebMKS, error) {
	return &c.mks, nil
}

// TestConsole opens a VM's console: a VMRC link always, and a single-use
// WebSocket that rvav proxies to the ESXi host when the WMKS client is
// configured.
func TestConsole(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		if _, err := h.store.Pool.Exec(ctx, `insert into project_members (project_id, user_id, role) values ('prj_web', 'usr_sbrunner', 'viewer')`); err != nil {
			t.Fatal(err)
		}
		esxi := fakeESXi(t)
		sum := sha256.Sum256(esxi.Certificate().Raw)
		source := consoleSource{Client: h.vc, mks: vsphere.WebMKS{URL: "wss://" + esxi.Listener.Addr().String() + "/ticket/good",
			Thumbprints: []string{hex.EncodeToString(sum[:])}}}
		h.svc.VCenter = func(context.Context) (core.ConsoleSource, error) { return source, nil }
		d := buildVM(h, simulator.VPX())
		path := "/api/v1/deployments/" + d.id + "/resources/" + d.resource + "/console"
		open := func(user string, want int) api.ConsoleSession {
			var s api.ConsoleSession
			var out any
			if want == 200 {
				out = &s
			}
			h.json("POST", path, user, nil, want, out)
			return s
		}

		// Viewers may not; it is not a request either.
		if a := d.actions("sbrunner")["console"]; a.Enabled {
			t.Fatalf("viewer's console = %+v", a)
		}
		open("sbrunner", 403)
		d.run("tnguyen", "console", nil, 422)

		// VMRC only, until the WMKS client is configured; the user signs in
		// to VMRC, as rvav's own session must never be cloned for them.
		s := open("tnguyen", 200)
		if s.VmrcUrl != "vmrc://"+h.vc.Host()+"/?moid="+d.vm().Moref || s.Web != nil {
			t.Fatalf("console = %+v", s)
		}
		// With a console-only account, the link carries a clone ticket of it.
		h.svc.ConsoleAccount = func(context.Context) (core.CloneTicketer, error) { return h.vc, nil }
		if s = open("tnguyen", 200); !strings.HasPrefix(s.VmrcUrl, "vmrc://clone:") || !strings.HasSuffix(s.VmrcUrl, "/?moid="+d.vm().Moref) {
			t.Fatalf("console with an account = %+v", s)
		}
		h.api.WMKSSDKURLs = []string{"https://vc01.corp.example/ui/webconsole/wmks.min.js"}
		s = open("tnguyen", 200)
		if s.Web == nil || !strings.HasPrefix(s.Web.Url, "/api/v1/console/") || len(s.Web.SdkUrls) != 1 {
			t.Fatalf("web console = %+v", s.Web)
		}

		// The WebSocket goes through rvav to the host and back.
		dial := func(p, origin string) (net.Conn, *http.Response, *bufio.Reader) {
			conn, err := net.Dial("tcp", strings.TrimPrefix(h.srv.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			key := base64.StdEncoding.EncodeToString([]byte(uuid.NewString()[:16]))
			fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Protocol: binary\r\n",
				p, strings.TrimPrefix(h.srv.URL, "http://"), key)
			if origin != "" {
				fmt.Fprintf(conn, "Origin: %s\r\n", origin)
			}
			fmt.Fprintf(conn, "\r\n")
			br := bufio.NewReader(conn)
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode == http.StatusSwitchingProtocols && resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(key) {
				t.Fatalf("accept = %q", resp.Header.Get("Sec-WebSocket-Accept"))
			}
			return conn, resp, br
		}
		conn, resp, br := dial(s.Web.Url, h.srv.URL)
		if resp.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("console socket = %s", resp.Status)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte("hello ESXi")); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len("hello ESXi"))
		if _, err := io.ReadFull(br, got); err != nil || string(got) != "hello ESXi" {
			t.Fatalf("echo = %q, %v", got, err)
		}
		conn.Close()

		// Tokens work once, from this site, to a host vCenter vouched for.
		if _, resp, _ := dial(s.Web.Url, ""); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("reused token = %s", resp.Status)
		}
		if _, resp, _ := dial(open("tnguyen", 200).Web.Url, "https://evil.example"); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("other origin = %s", resp.Status)
		}
		source.mks.Thumbprints = []string{strings.Repeat("AB", 32)}
		if _, resp, _ := dial(open("tnguyen", 200).Web.Url, ""); resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("unknown certificate = %s", resp.Status)
		}

		// A powered-off VM has no console.
		d.succeed("tnguyen", "power_off", nil)
		open("tnguyen", 409)
		var n int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action = 'console.open'`).Scan(&n); err != nil || n != 5 {
			t.Fatalf("audit has %d console opens (%v)", n, err)
		}
	})
}
