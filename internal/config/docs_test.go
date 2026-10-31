package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The configuration reference lists every setting the code reads, and only
// those (S14.2). Tests' own settings (RVAV_TEST_*, RVAV_AWX_TEST_*) and the
// variables rvav gives AWX (RVAV_URL, RVAV_TOKEN) aren't settings.
func TestEverySettingIsDocumented(t *testing.T) {
	read := map[string]bool{}
	for _, dir := range []string{"../../internal", "../../cmd", "../../web/nginx", "../../web/Dockerfile"} {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(p, "_test.go") || strings.Contains(p, "/e2e/") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			for _, m := range regexp.MustCompile(`"(RVAV_[A-Z0-9_]+)"|\$\{(RVAV_[A-Z0-9_]+)\}|^ENV (RVAV_[A-Z0-9_]+)|^\s+(RVAV_[A-Z0-9_]+)=`).
				FindAllStringSubmatch(string(b), -1) {
				for _, g := range m[1:] {
					if g != "" {
						read[g] = true
					}
				}
			}
			return nil
		})
	}
	for _, notSetting := range []string{"RVAV_URL", "RVAV_TOKEN"} {
		delete(read, notSetting)
	}
	for k := range read {
		if strings.HasPrefix(k, "RVAV_TEST_") || strings.HasPrefix(k, "RVAV_AWX_TEST_") {
			delete(read, k)
		}
	}
	doc, err := os.ReadFile("../../docs/operations/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("`(RVAV_[A-Z0-9_]+)`").FindAllStringSubmatch(string(doc), -1) {
		documented[m[1]] = true
	}
	var missing, extra []string
	for k := range read {
		if !documented[k] {
			missing = append(missing, k)
		}
	}
	for k := range documented {
		if !read[k] {
			extra = append(extra, k)
		}
	}
	slices.Sort(missing)
	slices.Sort(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("docs/operations/configuration.md: not listed %v; listed but not read %v", missing, extra)
	}
}

// The reference says where each setting is set (ADR-0030): in the tab of
// Administration › Integrations whose field its variable locks, or at
// install only.
func TestWhereEachSettingIsSet(t *testing.T) {
	panel := map[string]string{"oidc": "Identity provider", "vcenter": "vCenter", "console": "vCenter › Console", "awx": "AWX",
		"efficientip": "EfficientIP", "nsx": "NSX", "git": "Git", "vra": "vRA", "smtp": "Email",
		"teams": "Microsoft Teams", "slack": "Slack", "hosts": "Allowed hosts"}
	// Variables that lock what a tab does rather than a field: the token
	// rvav would make for AWX's inventory source (U1.4).
	inUI := map[string]string{"RVAV_INVENTORY_TOKEN": "Integrations › AWX"}
	for kind, fields := range Fields {
		if panel[kind] == "" {
			t.Errorf("no tab named for %s", kind)
		}
		for _, f := range fields {
			for _, env := range append([]string{f.Env}, alsoLockedBy[kind][f.Name]...) {
				inUI[env] = "Integrations › " + panel[kind]
			}
		}
	}
	doc, err := os.ReadFile("../../docs/operations/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile("(?m)^\\| `(RVAV_[A-Z0-9_]+)` \\|[^|]*\\|[^|]*\\| ([^|]*) \\|").FindAllStringSubmatch(string(doc), -1)
	listed := map[string]bool{}
	for _, m := range rows {
		env, where := m[1], m[2]
		listed[env] = true
		want := inUI[env]
		if want == "" {
			want = "install"
		}
		if where != want {
			t.Errorf("%s: Set in %q, want %q", env, where, want)
		}
	}
	for env := range inUI {
		if !listed[env] {
			t.Errorf("%s, set in %s, has no row", env, inUI[env])
		}
	}
	if len(rows) < len(inUI) {
		t.Errorf("%d rows for %d settings set in the UI", len(rows), len(inUI))
	}
}
