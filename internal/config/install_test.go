package config

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The System page lists the settings configuration.md says are set at
// install, in its order, and hides the values it marks secret (U1.5). The
// web container's own (RVAV_API_UPSTREAM, RVAV_WEB_*) are read by nginx,
// not by a Go process.
func TestInstallSettingsAreTheReference(t *testing.T) {
	doc, err := os.ReadFile("../../docs/operations/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	secret := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `(RVAV_[A-Z0-9_]+)` \\|[^|]*\\|\\s*([^|]*?)\\s*\\| install \\|").FindAllStringSubmatch(string(doc), -1) {
		if m[1] == "RVAV_API_UPSTREAM" || strings.HasPrefix(m[1], "RVAV_WEB_") {
			continue
		}
		want = append(want, m[1])
		secret[m[1]] = m[2] == "yes"
	}
	var got []string
	for _, s := range Install {
		got = append(got, s.Env)
		if s.Secret != secret[s.Env] {
			t.Errorf("%s: secret %v, the reference says %v", s.Env, s.Secret, secret[s.Env])
		}
		if s.Secret != (s.value == nil) && s.Env != "RVAV_DATABASE_URL" {
			t.Errorf("%s: a secret shows no value, others do", s.Env)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("Install = %v\nconfiguration.md's install rows = %v", got, want)
	}
}

func TestInstallSettingShown(t *testing.T) {
	t.Setenv("RVAV_SECRETS_KEY", "c2VjcmV0")
	t.Setenv("RVAV_TEMPORAL_ADDRESS", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range Install {
		v, set := s.Shown(c)
		switch s.Env {
		case "RVAV_SECRETS_KEY":
			if v != nil || !set {
				t.Errorf("secrets key = %v, %v", v, set)
			}
		case "RVAV_TEMPORAL_ADDRESS":
			if v == nil || *v != "localhost:7233" || set {
				t.Errorf("Temporal's address = %v, %v: the default, not set", v, set)
			}
		case "RVAV_DATABASE_URL":
			if v == nil || strings.Contains(*v, ":rvav@") {
				t.Errorf("database URL = %v", v)
			}
		}
	}
}

func TestRedactDatabaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://rvav:s3cret@db:5432/rvav?sslmode=disable": "postgres://rvav:xxxxx@db:5432/rvav?sslmode=disable",
		"postgres://rvav@db/rvav":                             "postgres://rvav@db/rvav",
		"postgres://db/rvav?password=s3cret&sslmode=require":  "postgres://db/rvav?password=xxxxx&sslmode=require",
		"host=db user=rvav password=s3cret dbname=rvav":       "host=db user=rvav password=xxxxx dbname=rvav",
		"host=db password = 'a b\\'c' dbname=rvav":            "host=db password = xxxxx dbname=rvav",
		"host=db PASSWORD=s3cret":                             "host=db PASSWORD=xxxxx",
		// A Unix socket's: no host, the directory in the query.
		"postgres://rvav:s3cret@/rvav?host=/var/run/postgresql": "postgres://rvav:xxxxx@/rvav?host=/var/run/postgresql",
		// The client key's password.
		"postgres://rvav:s3cret@db:5432/rvav?sslpassword=s3cret&sslmode=verify-full": "postgres://rvav:xxxxx@db:5432/rvav?sslmode=verify-full&sslpassword=xxxxx",
		"host=db sslpassword=s3cret sslkey=/k.pem":                                   "host=db sslpassword=xxxxx sslkey=/k.pem",
		"postgres://rvav:s3cret@db1:5432,db2:5432/rvav":                              "postgres://rvav:xxxxx@db1:5432,db2:5432/rvav",
		// Not a URL url.Parse reads: the password's / not escaped.
		"postgres://rvav:s3/cret@db/rvav": "postgres://xxxxx",
	} {
		if got := RedactDatabaseURL(in); got != want {
			t.Errorf("RedactDatabaseURL(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(RedactDatabaseURL(in), "s3") {
			t.Errorf("RedactDatabaseURL(%q) shows the password", in)
		}
	}
}
