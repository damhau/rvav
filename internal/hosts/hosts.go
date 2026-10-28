// Package hosts decides which hosts rvav may call on an administrator's
// behalf: REST value sources (RVAV_REST_SOURCE_HOSTS) and webhooks
// (RVAV_WEBHOOK_HOSTS).
package hosts

import (
	"net/url"
	"strings"
)

// Allowed says whether a URL's host is listed: over https, or over plain
// http when the host is listed as http://host (a development stand-in; a
// secret would travel in clear). *.example.com lists every subdomain.
func Allowed(list []string, u *url.URL) bool {
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	host, hostPort := strings.ToLower(u.Hostname()), strings.ToLower(u.Host)
	for _, h := range list {
		h = strings.ToLower(strings.TrimSpace(h))
		if plain, ok := strings.CutPrefix(h, "http://"); ok {
			h = plain
		} else if u.Scheme == "http" {
			continue
		}
		h = strings.TrimPrefix(h, "https://")
		switch {
		case h == "":
		case strings.HasPrefix(h, "*."):
			if strings.HasSuffix(host, h[1:]) {
				return true
			}
		case h == host || h == hostPort:
			return true
		}
	}
	return false
}
