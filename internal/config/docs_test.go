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
