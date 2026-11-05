package provision

import (
	"reflect"
	"testing"
)

// An AWX item's launch inputs are the job's inventory and credentials, not
// extra variables, whatever form their values take: a form's string, a
// binding's number (a float64 once through JSON), a list.
func TestTakeLaunchInputs(t *testing.T) {
	for _, tc := range []struct {
		vars      map[string]any
		inventory int
		creds     []int
		extraVars map[string]any
	}{
		{map[string]any{"role": "app", "inv": "3", "machine": "4"}, 3, []int{4}, map[string]any{"role": "app"}},
		{map[string]any{"inv": float64(1234567), "machine": []any{float64(4), "7", "x"}, "vault": 9}, 1234567, []int{4, 7, 9},
			map[string]any{}},
		{map[string]any{"role": "db"}, 0, nil, map[string]any{"role": "db"}},
		{map[string]any{"inv": 2.5}, 0, nil, map[string]any{}},
	} {
		j := Job{InventoryInput: "inv", CredentialInputs: []string{"machine", "vault"}}
		vars := map[string]any{}
		for k, v := range tc.vars {
			vars[k] = v
		}
		j.TakeLaunchInputs(vars)
		if j.Inventory != tc.inventory || !reflect.DeepEqual(j.Credentials, tc.creds) || !reflect.DeepEqual(vars, tc.extraVars) {
			t.Errorf("%v: inventory %d, credentials %v, extra variables %v", tc.vars, j.Inventory, j.Credentials, vars)
		}
	}
	// A job without launch inputs keeps every variable.
	j, vars := Job{}, map[string]any{"inventory": "3"}
	j.TakeLaunchInputs(vars)
	if j.Inventory != 0 || vars["inventory"] != "3" {
		t.Errorf("no launch inputs: inventory %d, variables %v", j.Inventory, vars)
	}
}
