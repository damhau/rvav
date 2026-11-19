package tfprovider

import (
	"reflect"
	"testing"
)

func TestUpdateInputs(t *testing.T) {
	form := func(version string) map[string]any {
		return map[string]any{"version_id": version,
			"schema": map[string]any{"properties": map[string]any{"size": map[string]any{}, "token": map[string]any{},
				"disks": map[string]any{}}},
			"ui_schema":      map[string]any{"fields": map[string]any{"token": map[string]any{"secret": true}}},
			"initial_values": map[string]any{"size": "S", "disks": 1}}
	}
	d := map[string]any{"version": map[string]any{"id": "v1"},
		"inputs": map[string]any{"size": "M", "token": masked, "gone": "x"}}
	given := map[string]any{"size": "L"}
	for _, tc := range []struct {
		name, version string
		want          map[string]any
	}{
		// On its own version, rvav keeps what the deployment has emptied.
		{"own version", "v1", map[string]any{"size": "L"}},
		{"new version", "v2", map[string]any{"size": "L", "disks": 1}},
	} {
		if got := updateInputs(form(tc.version), d, given); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestConfiguredInputs(t *testing.T) {
	given := map[string]any{"size": "M", "token": "s3cret", "proxy": nil, "hidden": "x"}
	has := map[string]any{"size": "L", "token": masked, "role": "app"}
	want := map[string]any{"size": "L", "token": "s3cret", "proxy": nil, "hidden": "x"}
	if got := configuredInputs(given, has); !reflect.DeepEqual(got, want) {
		t.Errorf("configuredInputs = %v, want %v", got, want)
	}
	if got := configuredInputs(nil, has); got != nil {
		t.Errorf("without inputs configured = %v", got)
	}
}
