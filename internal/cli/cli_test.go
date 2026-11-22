package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/damhau/rvav/pkg/rvavclient"
)

var schema = map[string]any{"properties": map[string]any{
	"name":     map[string]any{"type": "string"},
	"cpu":      map[string]any{"type": "integer"},
	"ratio":    map[string]any{"type": []any{"null", "number"}},
	"restart":  map[string]any{"type": "boolean"},
	"ports":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
	"roles":    map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []any{"web", "db"}}},
	"labels":   map[string]any{"type": "object"},
	"computed": map[string]any{"type": "string", "readOnly": true},
}, "required": []any{"name", "cpu"}}

func TestInputs(t *testing.T) {
	f := inputFlags{values: []string{"name=web-01", "cpu=4", "ratio=0.5", "restart=yes", "ports=80, 443", "roles=web,db",
		`labels={"team":"web"}`, "unknown=a=b", "empty="}}
	_, err := f.read(nil, schema)
	if err == nil || !strings.Contains(err.Error(), `--input restart: "yes" is not true or false`) {
		t.Fatalf("restart=yes = %v", err)
	}
	f.values[3] = "restart=true"
	got, err := f.read(nil, schema)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": "web-01", "cpu": int64(4), "ratio": 0.5, "restart": true, "ports": []any{int64(80), int64(443)},
		"roles": []any{"web", "db"}, "labels": map[string]any{"team": "web"}, "unknown": "a=b", "empty": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inputs = %#v", got)
	}
	for _, bad := range []string{"cpu=four", "ports=[80", "noequals", "=x", "ratio=half", "ports=80,x"} {
		if _, err := (inputFlags{values: []string{bad}}).read(nil, schema); err == nil {
			t.Errorf("--input %s should fail", bad)
		}
	}
	// JSON lists, and an empty list.
	got, _ = (inputFlags{values: []string{"ports=[22]", "roles="}}).read(nil, schema)
	if !reflect.DeepEqual(got["ports"], []any{float64(22)}) || !reflect.DeepEqual(got["roles"], []any{}) {
		t.Fatalf("JSON list = %#v", got)
	}

	// A file, then the flags over it; - is standard input.
	path := filepath.Join(t.TempDir(), "inputs.yaml")
	if err := os.WriteFile(path, []byte("name: from-file\ncpu: 2\nroles: [web]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = (inputFlags{file: path, values: []string{"cpu=8"}}).read(nil, schema)
	if err != nil || got["name"] != "from-file" || got["cpu"] != int64(8) || !reflect.DeepEqual(got["roles"], []any{"web"}) {
		t.Fatalf("file and flags = %#v, %v", got, err)
	}
	got, err = (inputFlags{file: "-"}).read(strings.NewReader(`{"name": "from-stdin"}`), schema)
	if err != nil || got["name"] != "from-stdin" {
		t.Fatalf("stdin = %#v, %v", got, err)
	}
	if _, err := (inputFlags{file: path + ".missing"}).read(nil, schema); err == nil {
		t.Fatal("a missing file should fail")
	}
	if got, _ := (inputFlags{file: "-"}).read(strings.NewReader(""), schema); got == nil {
		t.Fatal("an empty file is no inputs, not nil")
	}
}

func TestFormInputs(t *testing.T) {
	var ui rvavclient.UiSchema
	if err := json.Unmarshal([]byte(`{"layout":{"kind":"sections","sections":[{"key":"s","title":"S","fields":["roles","name","gone"]}]},
		"fields":{"name":{"label":"Name","widget":"text"},
		"labels":{"label":"Labels","widget":"keyvalue","value_source":{"kind":"rest"}},
		"cpu":{"label":"vCPU","widget":"select","options":[{"value":"2","label":"2"},{"value":"4","label":"4"}]}}}`), &ui); err != nil {
		t.Fatal(err)
	}
	got := formInputs(schema, ui, map[string]any{"cpu": float64(2), "roles": []any{"web"}})
	var names []string
	for _, in := range got {
		names = append(names, in.name)
	}
	if strings.Join(names, " ") != "roles name cpu labels ports ratio restart" {
		t.Fatalf("order = %v", names)
	}
	byName := map[string]formInput{}
	for _, in := range got {
		byName[in.name] = in
	}
	if r := byName["roles"]; r.kind != "list of string" || r.def != "web" || strings.Join(r.choices, ",") != "web,db" || r.required {
		t.Fatalf("roles = %+v", r)
	}
	if c := byName["cpu"]; c.kind != "integer" || !c.required || c.def != "2" || strings.Join(c.choices, ",") != "2,4" || c.label != "vCPU" {
		t.Fatalf("cpu = %+v", c)
	}
	if l := byName["labels"]; !l.computed || l.choices != nil {
		t.Fatalf("labels = %+v", l)
	}
	if r := byName["ratio"]; r.kind != "number" {
		t.Fatalf("ratio = %+v", r)
	}
}

func TestPickAction(t *testing.T) {
	vm := func(id, name string) rvavclient.Day2Action {
		return rvavclient.Day2Action{Key: "power_off", ResourceId: &id, ResourceName: &name}
	}
	two := []rvavclient.Day2Action{{Key: "edit"}, vm("res_1", "web-01"), vm("res_2", "web-02")}
	if a, err := pickAction(two, "edit", ""); err != nil || a.Key != "edit" {
		t.Fatalf("edit = %+v, %v", a, err)
	}
	if _, err := pickAction(two, "power_off", ""); err == nil || !strings.Contains(err.Error(), "web-01, web-02") {
		t.Fatalf("two VMs without --resource = %v", err)
	}
	if a, err := pickAction(two, "power_off", "WEB-02"); err != nil || *a.ResourceId != "res_2" {
		t.Fatalf("by name = %+v, %v", a, err)
	}
	if a, err := pickAction(two, "power_off", "res_1"); err != nil || *a.ResourceName != "web-01" {
		t.Fatalf("by ID = %+v, %v", a, err)
	}
	if _, err := pickAction(two, "power_off", "web-03"); err == nil || !strings.Contains(err.Error(), "on resource web-03") {
		t.Fatalf("unknown resource = %v", err)
	}
	if _, err := pickAction(two, "nope", ""); err == nil {
		t.Fatal("unknown action")
	}
}

func TestCollect(t *testing.T) {
	var asked []string
	read := func(cursor *string, n int) (page[int], error) {
		start := 0
		if cursor != nil {
			fmt.Sscan(*cursor, &start)
		}
		asked = append(asked, fmt.Sprintf("%d+%d", start, n))
		var items []int
		var raw []string
		for i := start; i < start+n && i < 450; i++ {
			items = append(items, i)
			raw = append(raw, fmt.Sprint(i))
		}
		body := `{"items":[` + strings.Join(raw, ",") + `]}`
		var next *string
		if start+n < 450 {
			next = ptr(fmt.Sprint(start + n))
		}
		return page[int]{items: items, body: []byte(body), next: next}, nil
	}
	items, raw, more, err := collect(250, read)
	if err != nil || len(items) != 250 || len(raw) != 250 || items[249] != 249 || !more || strings.Join(asked, " ") != "0+200 200+50" {
		t.Fatalf("collect 250 = %d items, %d raw, more %v, asked %v, %v", len(items), len(raw), more, asked, err)
	}
	asked = nil
	items, _, more, _ = collect(1000, read)
	if len(items) != 450 || more || strings.Join(asked, " ") != "0+200 200+200 400+200" {
		t.Fatalf("collect all = %d, more %v, asked %v", len(items), more, asked)
	}
	if items, _, more, _ = collect(450, read); len(items) != 450 || more {
		t.Fatalf("collect exactly all = %d, more %v", len(items), more)
	}
}

// TestTarget picks the rvav and the token: RVAV_TOKEN first, a profile's
// sign-in for its own rvav only.
func TestTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	ps, _ := rvavclient.LoadProfiles(path)
	ps.Profiles["default"] = &rvavclient.Profile{Server: "https://rvav.corp.example", Token: "rvav_pat_profile"}
	if err := ps.Save(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, profile, server string
		env                   map[string]string
		wantServer, wantToken string
		wantErr               string
	}{
		{name: "profile", wantServer: "https://rvav.corp.example", wantToken: "rvav_pat_profile"},
		{name: "same rvav", server: "https://rvav.corp.example/api/v1", wantServer: "https://rvav.corp.example", wantToken: "rvav_pat_profile"},
		{name: "another rvav", server: "https://evil.example", wantServer: "https://evil.example"},
		{name: "RVAV_URL elsewhere", env: map[string]string{"RVAV_URL": "https://other.example"}, wantServer: "https://other.example"},
		{name: "RVAV_TOKEN", env: map[string]string{"RVAV_URL": "https://ci.example", "RVAV_TOKEN": "rvav_pat_env"},
			wantServer: "https://ci.example", wantToken: "rvav_pat_env"},
		{name: "RVAV_TOKEN, profile's rvav", env: map[string]string{"RVAV_TOKEN": "rvav_pat_env"},
			wantServer: "https://rvav.corp.example", wantToken: "rvav_pat_env"},
		{name: "RVAV_TOKEN alone", profile: "none", env: map[string]string{"RVAV_TOKEN": "rvav_pat_env"}, wantErr: "RVAV_URL"},
		{name: "nothing", profile: "none", wantErr: "not signed in (profile \"none\")"},
	} {
		profile := c.profile
		if profile == "" {
			profile = "default"
		}
		a := &app{Env: Env{ConfigPath: path, Getenv: func(k string) string { return c.env[k] }}, profile: profile, server: c.server}
		server, tokens, err := a.target(context.Background())
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
			}
			continue
		}
		token := ""
		if tokens != nil {
			tok, _ := tokens.Token()
			token = tok.AccessToken
		}
		if err != nil || server != c.wantServer || token != c.wantToken {
			t.Errorf("%s: %q %q %v; want %q %q", c.name, server, token, err, c.wantServer, c.wantToken)
		}
	}
}

func TestMessage(t *testing.T) {
	warn := "warning"
	for err, want := range map[error]string{
		fmt.Errorf("%w: invalid_grant", rvavclient.ErrSignInExpired): "Sign in again: rvavctl login",
		&rvavclient.Error{Status: 401}:                               "run rvavctl login, or check RVAV_TOKEN",
		&rvavclient.Error{Status: 422, Problem: &rvavclient.Problem{Title: "Validation failed",
			Errors: &[]rvavclient.FieldError{{Code: &warn, Field: "/inputs/size", Message: "XL is large."}}}}: "add --accept-warnings",
		&rvavclient.Error{Status: 404}: "rvav answered 404 Not Found",
	} {
		if got := message(err); !strings.Contains(got, want) {
			t.Errorf("message(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestOutput(t *testing.T) {
	var out, stderr bytes.Buffer
	a := &app{Env: Env{Out: &out, Err: &stderr}, output: "json"}
	if err := a.list(nil, "No requests.", nil); err != nil || out.String() != "[]\n" {
		t.Fatalf("an empty JSON list = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := a.printJSON(rawJSON([]byte(`{"b":1,"a":[2]}`))); err != nil || out.String() != "{\n  \"b\": 1,\n  \"a\": [\n    2\n  ]\n}\n" {
		t.Fatalf("raw JSON keeps its order = %q, %v", out.String(), err)
	}
	a.output = "table"
	out.Reset()
	if err := a.list(nil, "No requests.", nil); err != nil || out.Len() != 0 || stderr.String() != "No requests.\n" {
		t.Fatalf("an empty table = %q %q, %v", out.String(), stderr.String(), err)
	}
	out.Reset()
	tb := newTable(&out, "ID", "TITLE")
	tb.row("req_1", "two\tcells\non two lines")
	if err := tb.flush(); err != nil || out.String() != "ID     TITLE\nreq_1  two cells on two lines\n" {
		t.Fatalf("table = %q", out.String())
	}
	out.Reset()
	if err := details(&out, "Name", "web-01", "Empty", "", "Status", "ready"); err != nil || out.String() != "Name:    web-01\nStatus:  ready\n" {
		t.Fatalf("details = %q", out.String())
	}
	// What a requester writes can't move the approver's cursor or hide a link.
	out.Reset()
	evil := "dev only\r\x1b[2KJustification: routine\x1b]8;;https://evil.example\x1b\\click\x1b]8;;\x1b\\ \u009b2K\x7f\x00 été"
	if err := details(&out, "Environment", "prd", "Justification", evil); err != nil {
		t.Fatal(err)
	}
	if want := "Environment:    prd\nJustification:  dev only �[2KJustification: routine�]8;;https://evil.example�\\click" +
		"�]8;;�\\ �2K�� été\n"; out.String() != want {
		t.Fatalf("details = %q, want %q", out.String(), want)
	}
	if got := multiline("Refused:\n  role: \x1b[31mred\x1b[0m"); got != "Refused:\n  role: �[31mred�[0m" {
		t.Fatalf("multiline = %q", got)
	}
	if (&app{output: "yaml"}).checkOutput() == nil {
		t.Fatal("-o yaml should fail")
	}
}
