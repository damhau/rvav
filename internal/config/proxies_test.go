package config

import (
	"strings"
	"testing"
)

func TestTrustedProxies(t *testing.T) {
	t.Setenv("RVAV_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.10,2001:db8::/32")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range c.TrustedProxies {
		got = append(got, p.String())
	}
	if strings.Join(got, " ") != "10.0.0.0/8 192.168.1.10/32 2001:db8::/32" {
		t.Errorf("RVAV_TRUSTED_PROXIES = %v", got)
	}
	t.Setenv("RVAV_TRUSTED_PROXIES", "10.0.0.0/8, the ingress")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), `"the ingress"`) {
		t.Errorf("a name in RVAV_TRUSTED_PROXIES: %v", err)
	}
}
