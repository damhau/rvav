package auth

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Client is where a request came from, as the audit log records it
// (ADR-0058).
type Client struct {
	IP        string
	UserAgent string
}

type clientKey struct{}

func WithClient(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// ClientFrom is the client WithClient set; none for work rvav does itself.
func ClientFrom(ctx context.Context) Client {
	c, _ := ctx.Value(clientKey{}).(Client)
	return c
}

// ClientOf reads where r came from: the peer's address; from a trusted
// proxy, the address that proxy (and those before it) saw, the rightmost
// in X-Forwarded-For that isn't one of them. Entries left of it may be
// made up by the client and are never believed.
func ClientOf(r *http.Request, trusted []netip.Prefix) Client {
	c := Client{UserAgent: clip(r.UserAgent(), 256)}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		c.IP = clip(host, 64)
		return c
	}
	ip = ip.Unmap()
	if isTrusted(ip, trusted) {
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				break
			}
			ip = hop.Unmap()
			if !isTrusted(ip, trusted) {
				break
			}
		}
	}
	c.IP = ip.String()
	return c
}

func isTrusted(ip netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Detail adds the client to an audit event's detail.
func (c Client) Detail(d map[string]any) map[string]any {
	if d == nil {
		d = map[string]any{}
	}
	if c.IP != "" {
		d["ip"] = c.IP
	}
	if c.UserAgent != "" {
		d["user_agent"] = c.UserAgent
	}
	return d
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
