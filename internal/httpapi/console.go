package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/vsphere"
)

// The VM console (ADR-0012): openConsole hands out a VMRC link and, when the
// organisation provides VMware's WMKS client, a single-use token. The
// browser opens a WebSocket on rvav with it, which rvav proxies to the ESXi
// host, so the browser never needs to reach the hosts.

const consoleTokenTTL = time.Minute

// putConsole keeps a console ticket behind a new token, in the database:
// the WebSocket may reach any API replica (S14.1).
func (s *Server) putConsole(ctx context.Context, mks vsphere.WebMKS, now time.Time) (string, time.Time, error) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	ticket, _ := json.Marshal(mks)
	expires := now.Add(consoleTokenTTL)
	hash := sha256.Sum256([]byte(token))
	return token, expires, s.Store.PutConsoleToken(ctx, hash[:], ticket, expires)
}

// takeConsole returns the ticket of a token once, while it is valid.
func (s *Server) takeConsole(ctx context.Context, token string, now time.Time) (vsphere.WebMKS, bool) {
	var mks vsphere.WebMKS
	hash := sha256.Sum256([]byte(token))
	ticket, expires, err := s.Store.TakeConsoleToken(ctx, hash[:])
	if err != nil || now.After(expires) || json.Unmarshal(ticket, &mks) != nil {
		return vsphere.WebMKS{}, false
	}
	return mks, true
}

func (s *Server) OpenConsole(w http.ResponseWriter, r *http.Request, deploymentID api.DeploymentIdPath, resourceID string) {
	web := len(s.WMKSSDKURLs) > 0
	c, err := s.Core.OpenConsole(r.Context(), principal(r), deploymentID, resourceID, web)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := api.ConsoleSession{VmrcUrl: c.VMRC}
	if c.Web != nil {
		token, expires, err := s.putConsole(r.Context(), *c.Web, time.Now())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Web = &struct {
			ExpiresAt time.Time `json:"expires_at"`
			SdkUrls   []string  `json:"sdk_urls"`
			Url       string    `json:"url"`
		}{ExpiresAt: expires, SdkUrls: s.WMKSSDKURLs, Url: "/api/v1/console/" + token}
	}
	writeJSON(w, http.StatusOK, out)
}

// ConsoleStream proxies the browser's WebSocket to the ESXi host, byte for
// byte after the handshake. The token is the credential: browsers can't
// send an Authorization header on a WebSocket.
func (s *Server) ConsoleStream(w http.ResponseWriter, r *http.Request, token string) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.problem(w, r, http.StatusBadRequest, "bad-request", "Not a WebSocket", "Open this URL with a WebSocket client.", nil)
		return
	}
	if !s.sameOrigin(r) {
		s.problem(w, r, http.StatusForbidden, "forbidden", "Not allowed from this origin", "", nil)
		return
	}
	mks, ok := s.takeConsole(r.Context(), token, time.Now())
	if !ok {
		s.problem(w, r, http.StatusNotFound, "not-found", "This console link has expired",
			"Console links work once, for a minute. Open the console again.", nil)
		return
	}
	upstream, br, resp, err := s.dialConsole(r, mks)
	if err != nil {
		s.Log.Warn("console upstream", "err", err, "request_id", requestID(r.Context()))
		s.problem(w, r, http.StatusBadGateway, "upstream", "The ESXi host did not accept the console", err.Error(), nil)
		return
	}
	defer upstream.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		s.problem(w, r, http.StatusInternalServerError, "internal", "This server can't proxy WebSockets", "", nil)
		return
	}
	client, cbuf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	// Hand the upstream's 101 to the browser: its Sec-WebSocket-Accept
	// matches the browser's key, which was passed on.
	fmt.Fprintf(cbuf, "HTTP/1.1 101 Switching Protocols\r\n")
	for k, vs := range resp.Header {
		for _, v := range vs {
			fmt.Fprintf(cbuf, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(cbuf, "\r\n")
	if err := cbuf.Flush(); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, cbuf); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, br); done <- struct{}{} }()
	<-done
}

// dialConsole opens the WebSocket to the ESXi host with the browser's
// handshake headers and returns the connection after the 101.
func (s *Server) dialConsole(r *http.Request, mks vsphere.WebMKS) (net.Conn, *bufio.Reader, *http.Response, error) {
	u, err := url.Parse(mks.URL)
	if err != nil {
		return nil, nil, nil, err
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	cfg := &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
	if len(mks.Thumbprints) > 0 {
		// ESXi certificates are often self-signed: trust the one vCenter vouches for.
		cfg.InsecureSkipVerify = true
		cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			return checkThumbprint(raw, mks.Thumbprints)
		}
	} else if s.ConsoleInsecure {
		cfg.InsecureSkipVerify = true
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", host, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n", u.RequestURI(), u.Host)
	fmt.Fprintf(&req, "Origin: https://%s\r\n", u.Hostname())
	for _, h := range []string{"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions"} {
		if v := r.Header.Get(h); v != "" {
			fmt.Fprintf(&req, "%s: %s\r\n", h, v)
		}
	}
	req.WriteString("\r\n")
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(conn, req.String()); err != nil {
		conn.Close()
		return nil, nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("ESXi answered %s", resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, br, resp, nil
}

// checkThumbprint accepts the host's certificate when its SHA-1 or SHA-256
// fingerprint is one vCenter gave with the ticket.
func checkThumbprint(raw [][]byte, thumbprints []string) error {
	if len(raw) == 0 {
		return errors.New("the ESXi host sent no certificate")
	}
	norm := func(s string) string { return strings.ToUpper(strings.ReplaceAll(s, ":", "")) }
	s1, s256 := sha1.Sum(raw[0]), sha256.Sum256(raw[0])
	have := []string{norm(hex.EncodeToString(s1[:])), norm(hex.EncodeToString(s256[:]))}
	for _, t := range thumbprints {
		if slices.Contains(have, norm(t)) {
			return nil
		}
	}
	return errors.New("the ESXi host's certificate is not the one vCenter vouched for")
}

// sameOrigin refuses WebSockets opened by pages of other sites.
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	return slices.Contains(s.CORSOrigins, origin)
}
