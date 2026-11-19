package vraimport

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/blueprint"
)

// quotedCloudConfig has inputs in the middle of texts: values, list
// items, quoted and block scalars, a flow sequence and a key.
const quotedCloudConfig = `#cloud-config
hostname: ${self.resourceName}
owner: ${input.app}
motd: 100% ${input.app} on ${self.resourceName}
runcmd:
  - echo /etc/${input.app}.conf
  - "touch '/srv/${input.app}'"
  - 'echo ${input.app} > /etc/it''s'
  - echo ${secret.token} ${input.app} ${resource.db.address}
  - echo '{{ x }}' ${input.app}
packages: [nginx, ${input.app}-tools]
${input.app}_dir: /srv
listen: port ${input.port}
write_files:
  - path: /etc/${input.app}/app.conf
    content: |
      app=${input.app}
      db=${resource.db.address}
      100%

  - path: /etc/motd
    content: >-
      Welcome to
      ${input.app}
# set by ${input.app}
`

const quotedUserData = `#cloud-config
hostname: {{ .vm.name }}
owner: {{ .inputs.app | json }}
motd: {{ printf "100%% %v on %v" .inputs.app .vm.name | json }}
runcmd:
  - {{ printf "echo /etc/%v.conf" .inputs.app | json }}
  - {{ printf "touch '/srv/%v'" .inputs.app | json }}
  - {{ printf "echo %v > /etc/it's" .inputs.app | json }}
  - {{ printf "echo %v %v %v" (secret "token") .inputs.app (index .components.db.addresses 0) | json }}
  - {{ printf "echo '{{ x }}' %v" .inputs.app | json }}
packages: [nginx, {{ printf "%v-tools" .inputs.app | json }}]
{{ printf "%v_dir" .inputs.app | json }}: /srv
listen: {{ printf "port %v" (.inputs.port | json) | json }}
write_files:
  - path: {{ printf "/etc/%v/app.conf" .inputs.app | json }}
    content: {{ printf "app=%v\ndb=%v\n100%%\n" .inputs.app (index .components.db.addresses 0) | json }}

  - path: /etc/motd
    content: {{ printf "Welcome to %v" .inputs.app | json }}
# set by {{ .inputs.app | json }}
`

// renderQuoted renders user data as the worker does, over a VM, the
// database's address, a secret and the inputs.
func renderQuoted(t *testing.T, ud string, inputs map[string]any) map[string]any {
	t.Helper()
	tpl, err := blueprint.ParseUserData(ud)
	if err != nil {
		t.Fatal(err)
	}
	tpl.Funcs(template.FuncMap{"secret": func(string) (string, error) { return "s3\"cr\net: x", nil }})
	var b bytes.Buffer
	if err := tpl.Execute(&b, map[string]any{"vm": map[string]any{"name": "web01", "index": 0},
		"components": map[string]any{"db": map[string]any{"addresses": []string{"10.0.0.5"}}}, "inputs": inputs}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("%v:\n%s", err, b.String())
	}
	return doc
}

// TestQuotedUserData: an input in the middle of a cloud-init text makes
// the whole YAML scalar one action through json, so that the text keeps
// no quotes and what a requester types never becomes YAML (#140).
func TestQuotedUserData(t *testing.T) {
	s := scope{machines: map[string]machineRef{"db": {key: "db"}}, numbers: map[string]bool{"port": true}}
	ud, problems := s.userData(quotedCloudConfig)
	if ud != quotedUserData || len(problems) != 0 {
		t.Fatalf("user data\n%s\nwant\n%s\nproblems %v", ud, quotedUserData, problems)
	}
	if err := blueprint.CheckUserData(ud); err != nil {
		t.Fatal(err)
	}
	const secret = "s3\"cr\net: x"
	for _, app := range []string{"shop", "x\nruncmd:\n  - rm -rf /", `a" #b: 'c'`, "%s %v {{ .x }}", "- [z]\n\nk: |\n  v", "\u2028y"} {
		want := map[string]any{
			"hostname": "web01",
			"owner":    app,
			"motd":     "100% " + app + " on web01",
			"runcmd": []any{"echo /etc/" + app + ".conf", "touch '/srv/" + app + "'", "echo " + app + " > /etc/it's",
				"echo " + secret + " " + app + " 10.0.0.5", "echo '{{ x }}' " + app},
			"packages":   []any{"nginx", app + "-tools"},
			app + "_dir": "/srv",
			"listen":     "port 1048576",
			"write_files": []any{
				map[string]any{"path": "/etc/" + app + "/app.conf", "content": "app=" + app + "\ndb=10.0.0.5\n100%\n"},
				map[string]any{"path": "/etc/motd", "content": "Welcome to " + app},
			},
		}
		if got := renderQuoted(t, ud, map[string]any{"app": app, "port": float64(1048576)}); !reflect.DeepEqual(got, want) {
			t.Errorf("app %q:\n%#v\nwant\n%#v", app, got, want)
		}
	}
}

// TestUnquotedUserData: where rvav can't rewrite a scalar, the input is
// printed through json in place, and the plan says so; a script isn't
// YAML.
func TestUnquotedUserData(t *testing.T) {
	for _, c := range []struct{ src, want, problem string }{
		{"#cloud-config\nmotd: hello\n  ${input.app}\n", "#cloud-config\nmotd: hello\n  {{ .inputs.app | json }}\n", "can't print as one"},
		{"#cloud-config\nmotd: |2\n    a ${input.app}\n", "#cloud-config\nmotd: |2\n    a {{ .inputs.app | json }}\n", "can't print as one"},
		{"#cloud-config\nmotd: |+\n  a ${input.app}\n\n", "#cloud-config\nmotd: |+\n  a {{ .inputs.app | json }}\n\n", "can't print as one"},
		{"#cloud-config\nmotd: !!str a ${input.app}\n", "#cloud-config\nmotd: !!str a {{ .inputs.app | json }}\n", "can't print as one"},
		{"#cloud-config\nmotd: &m |\n  a ${input.app}\nissue: *m\n", "#cloud-config\nmotd: &m |\n  a {{ .inputs.app | json }}\nissue: *m\n", "can't print as one"},
		{"#cloud-config\nmotd: [a ${input.app}\n", "#cloud-config\nmotd: [a {{ .inputs.app | json }}\n", "isn't YAML rvav can read"},
		{"#!/bin/sh\necho /etc/${input.app}.conf\n", "#!/bin/sh\necho /etc/{{ .inputs.app | json }}.conf\n", ""},
		{"#cloud-config\nmotd: |\n  ${self.resourceName} at ${resource.db.address}\n",
			"#cloud-config\nmotd: |\n  {{ .vm.name }} at {{ index .components.db.addresses 0 }}\n", ""},
	} {
		s := scope{machines: map[string]machineRef{"db": {key: "db"}}}
		ud, problems := s.userData(c.src)
		if ud != c.want || c.problem == "" && len(problems) > 0 || c.problem != "" && (len(problems) != 1 || !strings.Contains(problems[0], c.problem)) {
			t.Errorf("%q: %q, %q; want %q, %q", c.src, ud, problems, c.want, c.problem)
		}
		if err := blueprint.CheckUserData(ud); err != nil {
			t.Errorf("%q: %v", c.src, err)
		}
	}
}
