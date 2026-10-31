package config

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// uiVariables are the variables that lock something Administration sets.
func uiVariables() map[string]bool {
	out := map[string]bool{}
	for _, v := range Variables() {
		out[v] = true
	}
	if !out["RVAV_VCENTER_URL"] || !out["RVAV_VRA_PASSWORD"] || !out["RVAV_INVENTORY_TOKEN"] {
		panic("Variables misses some")
	}
	return out
}

func readYAML(t *testing.T, path string, out any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

type composeFile struct {
	Env      map[string]string `yaml:"x-rvav-env"`
	Fresh    map[string]string `yaml:"x-fresh-env"`
	Services map[string]struct {
		Command     []string          `yaml:"command"`
		Environment map[string]string `yaml:"environment"`
	} `yaml:"services"`
}

// deploy/docker-compose.fresh.yml starts a new install (U1.5): rvav's
// processes get none of the integrations' variables the base file sets
// (an empty one is unset), sign in with OIDC, and migrate without the
// seed. The fakes keep theirs.
func TestComposeFreshStart(t *testing.T) {
	var base, fresh composeFile
	readYAML(t, "../../deploy/docker-compose.yml", &base)
	readYAML(t, "../../deploy/docker-compose.fresh.yml", &fresh)
	ui := uiVariables()
	for _, name := range []string{"migrate", "api", "worker", "sync"} {
		svc, ok := fresh.Services[name]
		if !ok {
			t.Fatalf("the fresh start leaves %s as it is", name)
		}
		env := maps.Clone(base.Services[name].Environment)
		maps.Copy(env, svc.Environment)
		for _, k := range slices.Sorted(maps.Keys(env)) {
			if ui[k] && env[k] != "" {
				t.Errorf("%s: %s = %q, set in Administration on a new install", name, k, env[k])
			}
		}
		if env["RVAV_AUTH_MODE"] != "oidc" || env["RVAV_ALLOW_DEV_AUTH"] != "" {
			t.Errorf("%s signs in with %q (dev allowed: %q)", name, env["RVAV_AUTH_MODE"], env["RVAV_ALLOW_DEV_AUTH"])
		}
	}
	if cmd := fresh.Services["migrate"].Command; slices.ContainsFunc(cmd, func(a string) bool { return a != "migrate" }) {
		t.Errorf("migrate = %v, want the schema only", cmd)
	}
	for _, name := range []string{"vcsim-bootstrap", "efficientip", "nsx"} {
		if _, ok := fresh.Services[name]; ok {
			t.Errorf("the fresh start changes %s, a fake with its own settings", name)
		}
	}
	if base.Env["RVAV_VCENTER_URL"] == "" || base.Services["api"].Environment["RVAV_VCENTER_URL"] == "" {
		t.Fatal("the base file's environment wasn't read")
	}
}

// The chart's values and the VM install's example set only the install
// settings (U1.5): an integration's variable would lock its field in
// Administration.
func TestChartValuesAreInstallSettings(t *testing.T) {
	var values struct {
		Config map[string]any `yaml:"config"`
		Secret struct {
			Env map[string]any `yaml:"env"`
		} `yaml:"secret"`
	}
	readYAML(t, "../../deploy/helm/rvav/values.yaml", &values)
	ui := uiVariables()
	for _, m := range []map[string]any{values.Config, values.Secret.Env} {
		for k := range m {
			if ui[k] {
				t.Errorf("values.yaml sets %s, which locks its field in Administration", k)
			}
		}
	}
	if values.Config["RVAV_PUBLIC_URL"] == nil || values.Secret.Env["RVAV_DATABASE_URL"] == nil {
		t.Fatalf("values = %+v", values)
	}

	// The VM install's example too.
	b, err := os.ReadFile("../../deploy/vm/rvav.env.example")
	if err != nil {
		t.Fatal(err)
	}
	set := 0
	for _, line := range strings.Split(string(b), "\n") {
		k, _, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		set++
		if ui[k] {
			t.Errorf("rvav.env.example sets %s, which locks its field in Administration", k)
		}
	}
	if set < 5 {
		t.Fatalf("rvav.env.example sets %d settings", set)
	}
}

// The chart's notes give a way to the local administrator that doesn't
// depend on the log of the container that made it: applying a connection
// starts that container again, and the line is gone.
func TestChartNotesResetPassword(t *testing.T) {
	b, err := os.ReadFile("../../deploy/helm/rvav/templates/NOTES.txt")
	if err != nil {
		t.Fatal(err)
	}
	const reset = "kubectl -n {{ .Release.Namespace }} exec deploy/{{ .Release.Name }}-api -c api -- rvav admin reset-password"
	notes := string(b)
	if !strings.Contains(notes, reset) {
		t.Fatalf("NOTES.txt doesn't give %q:\n%s", reset, notes)
	}
	if i := strings.Index(notes, reset); strings.LastIndex(notes[:i], "{{- end }}") < strings.LastIndex(notes[:i], "{{- if .Values.secret.env.RVAV_ADMIN_PASSWORD }}") {
		t.Fatal("the reset is given only when the password was generated: give it either way")
	}
}
