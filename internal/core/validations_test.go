package core

import (
	"reflect"
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
)

func TestReadCheck(t *testing.T) {
	for _, c := range []struct {
		name string
		in   any
		want []checkMessage
		err  bool
	}{
		{"valid", map[string]any{"valid": true, "message": "ignored"}, nil, false},
		{"nothing to say", map[string]any{}, nil, false},
		{"refused without a word", map[string]any{"valid": false}, []checkMessage{{}}, false},
		{"a message", map[string]any{"message": " Over budget. "}, []checkMessage{{message: "Over budget."}}, false},
		{"errors by field", map[string]any{"valid": false, "errors": map[string]any{"cpu": "Too many.", "budget": "Too much.", "ok": nil}},
			[]checkMessage{{field: "budget", message: "Too much."}, {field: "cpu", message: "Too many."}}, false},
		{"a list of errors", map[string]any{"errors": []any{map[string]any{"field": "owner", "message": "Not you."}, "Closed."}},
			[]checkMessage{{field: "owner", message: "Not you."}, {message: "Closed."}}, false},
		{"valid that isn't a boolean", map[string]any{"valid": "yes"}, nil, true},
	} {
		got, err := readCheck(c.in, "$.valid", "$.message", "$.errors")
		if (err != nil) != c.err || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: readCheck = %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
	if _, err := readCheck(map[string]any{}, "$[", "$.message", "$.errors"); err == nil {
		t.Error("a path that isn't JSONPath was read")
	}
	got, _ := readCheck(map[string]any{"result": map[string]any{"ok": false, "why": "Closed."}}, "$.result.ok", "$.result.why", "$.errors")
	if !reflect.DeepEqual(got, []checkMessage{{message: "Closed."}}) {
		t.Errorf("custom paths = %+v", got)
	}
}

func TestCELCheck(t *testing.T) {
	vars := policy.Vars{"inputs": map[string]any{"cpu": 16.0, "owner": "usr_tnguyen"}, "user": map[string]any{"id": "usr_tnguyen"}}
	for _, c := range []struct {
		expr string
		want []checkMessage
	}{
		{`inputs.cpu <= 32.0`, nil},
		{`inputs.cpu <= 8.0`, []checkMessage{{}}},
		{`inputs.cpu <= 8.0 ? "" : "At most 8 CPUs."`, []checkMessage{{message: "At most 8 CPUs."}}},
		{`inputs.cpu <= 8.0 ? {} : {"cpu": "At most 8.", "owner": inputs.owner == user.id ? "Not you." : ""}`,
			[]checkMessage{{field: "cpu", message: "At most 8."}, {field: "owner", message: "Not you."}}},
		{`["One.", "Two."]`, []checkMessage{{message: "One."}, {message: "Two."}}},
		{`null`, nil},
	} {
		got, err := celCheck(c.expr, vars, policy.EvalSource)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %+v, %v; want %+v", c.expr, got, err, c.want)
		}
	}
	if _, err := celCheck(`inputs.cpu`, vars, policy.EvalSource); err == nil {
		t.Error("a number passed or refused")
	}
}

func TestBoundInput(t *testing.T) {
	bindings := map[string]any{"cc": "inputs.costCentre", "env": ` inputs["environment"] `, "both": "inputs.a + inputs.b", "env2": "environment"}
	for param, want := range map[string]string{"cc": "costCentre", "env": "environment", "both": "", "env2": "", "none": ""} {
		if got := boundInput(bindings, param); got != want {
			t.Errorf("boundInput(%s) = %q, want %q", param, got, want)
		}
	}
	if got := refusedText("", "Budget"); got != "Budget refused the request." {
		t.Errorf("refusedText = %q", got)
	}
}

func TestCheckValidations(t *testing.T) {
	props := map[string]any{"cpu": map[string]any{"type": "number"}, "owner": map[string]any{"type": "string"}}
	good := []api.FormValidation{
		{Key: "budget", Kind: api.Rest, Params: &map[string]any{"url": "https://budget.corp.example/check?cpu=${inputs.cpu}", "method": "POST"}},
		{Key: "size", Kind: api.Cel, Fields: &[]string{"cpu"}, Params: &map[string]any{"check": "inputs.cpu <= 8.0"}},
		{Key: "cc", Kind: api.Library, Name: present.Ptr("costCentreOpen"), Version: new(2), Params: &map[string]any{"cc": "inputs.owner"}},
	}
	if errs := checkValidations(api.UiSchema{Validations: &good}, props, "/definition"); len(errs) != 0 {
		t.Fatalf("good validations = %+v", errs)
	}
	bad := []api.FormValidation{
		{Key: "1x", Kind: api.Cel, Params: &map[string]any{"check": "true"}},
		{Key: "size", Kind: api.Cel, Fields: &[]string{"memory"}, Params: &map[string]any{}},
		{Key: "size", Kind: api.Rest, Params: &map[string]any{"url": "https://x.corp.example/${params.cc}", "valid": "$["}},
		{Key: "cc", Kind: api.Library, Params: &map[string]any{"cc": 3}},
		{Key: "odd", Kind: api.Builtin},
	}
	var got []string
	for _, e := range checkValidations(api.UiSchema{Validations: &bad}, props, "/definition") {
		got = append(got, e.Field)
	}
	at := "/definition/ui_schema/validations/"
	want := []string{at + "0/key", at + "1/fields/0", at + "1/params/check", at + "2/key", at + "2/params/url", at + "2/params/valid",
		at + "3/name", at + "3/version", at + "3/params/cc", at + "4/kind"}
	if !slices.Equal(got, want) {
		t.Fatalf("errors at\n %v, want\n %v", got, want)
	}
}

func TestCheckEntry(t *testing.T) {
	src := api.FormLibraryEntryWrite{Name: "Versions", Use: api.Source, Kind: api.Rest, Parameters: []api.FormLibraryParameter{{Name: "env", Label: "Environment"}},
		Params: map[string]any{"url": "https://dbaas.corp.example/versions?env=${params.env}", "items": "$[*]", "value": "$.v"}}
	if errs := checkEntry(src, ""); len(errs) != 0 {
		t.Fatalf("a good source = %+v", errs)
	}
	check := api.FormLibraryEntryWrite{Name: "Open", Use: api.Validation, Kind: api.Cel, Params: map[string]any{"check": `params.cc.startsWith("CC-")`}}
	if errs := checkEntry(check, ""); len(errs) != 0 {
		t.Fatalf("a good validation = %+v", errs)
	}
	for _, c := range []struct {
		name string
		w    api.FormLibraryEntryWrite
		want []string
	}{
		{"an entry reading a form", api.FormLibraryEntryWrite{Name: "x", Use: api.Source, Kind: api.Rest, Params: map[string]any{"url": "https://a.corp.example/${inputs.env}", "items": "$[*]", "value": "$.v"}},
			[]string{"/params/url"}},
		{"a CEL entry reading a form", api.FormLibraryEntryWrite{Name: "x", Use: api.Validation, Kind: api.Cel, Params: map[string]any{"check": "inputs.cpu > 1.0"}},
			[]string{"/params/check"}},
		{"a library entry", api.FormLibraryEntryWrite{Name: "x", Use: api.Source, Kind: api.Library}, []string{"/kind"}},
		{"parameters", api.FormLibraryEntryWrite{Name: " ", Use: "both", Kind: api.Cel, Params: map[string]any{"options": "[]"},
			Parameters: []api.FormLibraryParameter{{Name: "a", Label: "A"}, {Name: "a", Label: ""}, {Name: "_b", Label: "B"}}},
			[]string{"/name", "/use", "/parameters/1/name", "/parameters/1/label", "/parameters/2/name"}},
		{"a source's message", api.FormLibraryEntryWrite{Name: "x", Use: api.Source, Kind: api.Cel, Params: map[string]any{"options": "[]"}, Message: present.Ptr("No.")},
			[]string{"/message"}},
		{"a validation that searches", api.FormLibraryEntryWrite{Name: "x", Use: api.Validation, Kind: api.Cel, Params: map[string]any{"check": "true"}, Search: new(true)},
			[]string{"/search"}},
	} {
		var got []string
		for _, e := range checkEntry(c.w, "") {
			got = append(got, e.Field)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: errors at %v, want %v", c.name, got, c.want)
		}
	}
	inline := api.ValueSource{Kind: api.Rest, Params: &map[string]any{"url": "https://a.corp.example/${params.env}", "items": "$[*]", "value": "$.v"}}
	if errs := checkCustomSource(inline, "/value_source"); len(errs) != 1 || errs[0].Field != "/value_source/params/url" {
		t.Errorf("an inline source reading params = %+v", errs)
	}
}

func TestBumpLibrary(t *testing.T) {
	src := func(name string, version int) *api.ValueSource {
		return &api.ValueSource{Kind: api.Library, Name: &name, Version: &version}
	}
	d := api.CatalogVersionDefinition{UiSchema: api.UiSchema{
		Fields: map[string]api.UiField{"a": {ValueSource: src("dbVersions", 1)}, "b": {ValueSource: src("dbVersions", 3)}, "c": {ValueSource: src("other", 1)}},
		Validations: &[]api.FormValidation{
			{Key: "v", Kind: api.Library, Name: present.Ptr("dbVersions"), Version: new(2)},
			{Key: "w", Kind: api.Cel, Params: &map[string]any{"check": "true"}},
		},
	}}
	if !bumpLibrary(&d, "dbVersions", 3) {
		t.Fatal("nothing bumped")
	}
	f := d.UiSchema.Fields
	if *f["a"].ValueSource.Version != 3 || *f["b"].ValueSource.Version != 3 || *f["c"].ValueSource.Version != 1 || *(*d.UiSchema.Validations)[0].Version != 3 {
		t.Fatalf("bumped = %+v", d.UiSchema)
	}
	if bumpLibrary(&d, "dbVersions", 3) {
		t.Fatal("bumped twice")
	}
}

func TestLibraryVars(t *testing.T) {
	got := libraryVars(map[string]any{"env": "dev"}, policy.Vars{"inputs": map[string]any{"a": 1}, "propgroup": map[string]any{}, "environment": "dev", "user": map[string]any{"id": "u"}})
	want := policy.Vars{"params": map[string]any{"env": "dev"}, "environment": "dev", "user": map[string]any{"id": "u"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("libraryVars = %v", got)
	}
}
