package tfprovider

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func TestTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	ps := &rvavclient.Profiles{Path: path, Profiles: map[string]*rvavclient.Profile{
		"default": {Server: "https://rvav.corp.example", Token: "rvav_pat_desk"},
		"lab":     {Server: "https://rvav-lab.corp.example"},
	}}
	if err := ps.Save(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                string
		url, token, profile string
		env                 map[string]string
		server, sent, err   string
	}{
		{name: "token given", url: "https://other.example", token: "rvav_pat_ci",
			server: "https://other.example", sent: "rvav_pat_ci"},
		{name: "environment", env: map[string]string{"RVAV_URL": "https://ci.example", "RVAV_TOKEN": "rvav_pat_env"},
			server: "https://ci.example", sent: "rvav_pat_env"},
		{name: "given beats environment", token: "rvav_pat_ci", env: map[string]string{"RVAV_URL": "https://ci.example", "RVAV_TOKEN": "rvav_pat_env"},
			server: "https://ci.example", sent: "rvav_pat_ci"},
		{name: "token without address", token: "rvav_pat_ci", err: "no address"},
		{name: "default profile", server: "https://rvav.corp.example", sent: "rvav_pat_desk"},
		{name: "profile's own rvav", url: "https://rvav.corp.example/api/v1", server: "https://rvav.corp.example", sent: "rvav_pat_desk"},
		{name: "profile's token stays home", url: "https://elsewhere.example", server: "https://elsewhere.example"},
		{name: "named profile in development mode", profile: "lab", server: "https://rvav-lab.corp.example"},
		{name: "missing profile", profile: "prod", err: `no rvavctl profile "prod"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &rvavProvider{configPath: path, getenv: func(k string) string { return c.env[k] }}
			cfg := providerModel{URL: str0(c.url), Token: str0(c.token), Profile: str0(c.profile)}
			server, tokens, err := p.target(cfg, http.DefaultClient)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if server != c.server {
				t.Errorf("server = %q, want %q", server, c.server)
			}
			sent := ""
			if tokens != nil {
				tok, err := tokens.Token()
				if err != nil {
					t.Fatal(err)
				}
				sent = tok.AccessToken
			}
			if sent != c.sent {
				t.Errorf("token = %q, want %q", sent, c.sent)
			}
		})
	}
	t.Run("no rvav at all", func(t *testing.T) {
		p := &rvavProvider{configPath: filepath.Join(t.TempDir(), "none.json"), getenv: func(string) string { return "" }}
		if _, _, err := p.target(providerModel{}, http.DefaultClient); err == nil || !strings.Contains(err.Error(), "no rvav to call") {
			t.Fatalf("err = %v", err)
		}
	})
}

func str0(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
