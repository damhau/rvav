package core

import (
	"reflect"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/config"
)

// What is read on next use restarts no process when it changes; a
// connection does (ADR-0030). Active Directory and ServiceNow restarted
// every process, though both are read on next use (#211).
func TestOnlyConnectionsRestart(t *testing.T) {
	sample := map[string]any{"string": "https://changed.corp.example", "bool": true, "int": 7.0, "list": []any{"changed.corp.example"},
		"duration": "7m"}
	for _, kind := range integrationKinds {
		for _, f := range config.Fields[kind] {
			t.Setenv(f.Env, "")
		}
	}
	for _, kind := range integrationKinds {
		settings := map[string]any{}
		for _, f := range config.Fields[kind] {
			if !f.Secret {
				settings[f.Name] = sample[f.Type]
			}
		}
		var before, after config.Config
		after.ApplyStored(kind, config.Stored{Settings: settings, Secret: "changed"})
		if reflect.DeepEqual(before, after) {
			t.Fatalf("%s: the sample settings change nothing", kind)
		}
		restarts := !reflect.DeepEqual(ConnectionsOf(before), ConnectionsOf(after))
		if live := slices.Contains(LiveKinds, kind); restarts == live {
			t.Errorf("%s, read on next use %v: a change restarts the processes %v", kind, live, restarts)
		}
	}
}
