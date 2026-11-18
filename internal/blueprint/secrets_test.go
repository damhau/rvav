package blueprint

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestUserDataSecrets(t *testing.T) {
	refs, err := UserDataSecrets("#cloud-config\nusers:\n  - name: app\n    passwd: {{ secret \"app-pw\" | json }}\n" +
		"{{ if .inputs.ldap }}ldap: {{ secret \"ldap-bind\" }}{{ end }}\n{{ $t := secret \"app-pw\" }}")
	if err != nil {
		t.Fatal(err)
	}
	want := []SecretRef{{Name: "app-pw", Line: 4}, {Name: "ldap-bind", Line: 5}, {Name: "app-pw", Line: 6}}
	if !slices.Equal(refs, want) {
		t.Fatalf("refs = %+v", refs)
	}
	for _, text := range []string{
		"a: {{ secret .inputs.which }}",
		"a: {{ \"app-pw\" | secret }}",
		"a: {{ secret (printf \"%s\" \"x\") }}",
		"a: {{ with secret \"x\" }}{{ . }}{{ end }}\nb: {{ call secret \"x\" }}",
	} {
		if _, err := UserDataSecrets(text); err == nil || !strings.Contains(err.Error(), "name the secret in quotes") {
			t.Errorf("%q: err = %v", text, err)
		}
	}
	if err := CheckUserData("a: {{ secret .inputs.which | json }}"); err == nil {
		t.Error("CheckUserData takes a secret the requester chooses")
	}
}

func TestBindingSecret(t *testing.T) {
	for v, want := range map[any]string{"${secret.db-pw}": "db-pw", " ${ secret.awx-token } ": "awx-token", "${secret.x}": "",
		"pre ${secret.db-pw}": "", "${secret.db-pw} post": "", "${secret.Db}": "", 42: ""} {
		got, ok := BindingSecret(v)
		if got != want || ok != (want != "") {
			t.Errorf("%v = %q %v", v, got, ok)
		}
	}
}

func TestBlueprintSecrets(t *testing.T) {
	raw := json.RawMessage(`{"kind": "composite", "steps": [
		{"key": "a", "awx": {"job_template": "t"}, "with": {"token": "${secret.awx-token}", "role": "inputs.role"},
		 "undo": {"job_template": "u", "with": {"token": "${secret.undo-token}"}}},
		{"key": "b", "workflow": "w", "with": {"pw": "${secret.not-here}"}},
		{"key": "c", "branch": {"cases": [{"when": "true", "steps": [{"key": "d", "awx": {"job_template": "t"},
			"with": {"pw": "${secret.deep}"}}]}]}},
		{"key": "e", "notify": {"requester": true, "message": "it is ${secret.awx-token}"}}]}`)
	var b Blueprint
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range b.Secrets() {
		got = append(got, r.Name+"@"+r.Pointer)
	}
	want := []string{"awx-token@/steps/0/with/token", "undo-token@/steps/0/undo/with/token", "deep@/steps/2/branch/cases/0/steps/0/with/pw"}
	if !slices.Equal(got, want) {
		t.Fatalf("secrets = %v", got)
	}
	if bad := MisplacedSecrets(raw); !slices.Equal(bad, []string{"/steps/1/with/pw", "/steps/3/notify/message"}) {
		t.Fatalf("misplaced = %v", bad)
	}

	vm := json.RawMessage(`{"kind": "vm", "guest": {"os_family": "linux", "cloud_init": {"user_data": "#cloud-config\na: {{ secret \"ci-pw\" }}"}},
		"properties": {"pw": "${secret.ci-pw}"}}`)
	b = Blueprint{}
	if err := json.Unmarshal(vm, &b); err != nil {
		t.Fatal(err)
	}
	if refs := b.Secrets(); len(refs) != 1 || refs[0] != (SecretRef{Name: "ci-pw", Pointer: "/guest/cloud_init/user_data", Line: 2}) {
		t.Fatalf("vm secrets = %+v", refs)
	}
	if bad := MisplacedSecrets(vm); !slices.Equal(bad, []string{"/properties/pw"}) {
		t.Fatalf("vm misplaced = %v", bad)
	}
}
