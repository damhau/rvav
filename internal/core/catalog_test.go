package core

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

func TestRESTAllowedSchemes(t *testing.T) {
	r := NewRESTSources([]string{"cmdb.corp.example", "*.svc.corp.example", "http://stand-in:8080"})
	for raw, want := range map[string]bool{
		"https://cmdb.corp.example/x":  true,
		"http://cmdb.corp.example/x":   false, // https only unless listed as http://
		"https://a.svc.corp.example/x": true,
		"http://stand-in:8080/x":       true,
		"https://stand-in:8080/x":      true,
		"http://stand-in:9090/x":       false,
		"https://other.example/x":      false,
		"ftp://cmdb.corp.example/x":    false,
	} {
		u, _ := url.Parse(raw)
		if got := r.Allowed(t.Context(), u); got != want {
			t.Errorf("Allowed(%s) = %v, want %v", raw, got, want)
		}
	}
	// Inputs can't climb the path.
	_, _, err := r.Options(context.Background(), map[string]any{"url": "https://cmdb.corp.example/api/${inputs.app}/items"},
		inputRefs(map[string]any{"app": ".."}), "", nil)
	if err == nil || !strings.Contains(err.Error(), `".."`) {
		t.Fatalf("an input of .. = %v", err)
	}
	// Nor can the searched text.
	_, _, err = r.Options(context.Background(), map[string]any{"url": "https://cmdb.corp.example/api/${search}/items"},
		nil, ".", nil)
	if err == nil || !strings.Contains(err.Error(), "The search") {
		t.Fatalf("a search for . = %v", err)
	}
}

func TestFillSearch(t *testing.T) {
	inputs := inputRefs(map[string]any{"app": "${search}", "env": "dev"})
	if got, want := fill("https://h/apps?q=${search}&app=${inputs.app}&env=${inputs.env}", inputs, "pay roll", url.QueryEscape),
		"https://h/apps?q=pay+roll&app=%24%7Bsearch%7D&env=dev"; got != want {
		t.Errorf("fill URL = %s, want %s", got, want)
	}
	if got, want := fill(`{"q": "${search}", "app": "${inputs.app}"}`, inputs, `a"b`, jsonEscape),
		`{"q": "a\"b", "app": "${search}"}`; got != want {
		t.Errorf("fill body = %s, want %s", got, want)
	}
}

func TestFormSchemaRefsStayInside(t *testing.T) {
	_, err := compileSchemaUncached(json.RawMessage(`{"type": "object", "properties": {"x": {"$ref": "file:///etc/hostname"}}}`))
	if err == nil {
		t.Fatal("a $ref to a file compiled")
	}
	_, err = compileSchemaUncached(json.RawMessage(`{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
		"$defs": {"name": {"type": "string"}}, "properties": {"x": {"$ref": "#/$defs/name"}}}`))
	if err != nil {
		t.Fatalf("a $ref inside the schema: %v", err)
	}
}

func TestNextNumber(t *testing.T) {
	rows := func(numbers ...string) []store.VersionRow {
		out := []store.VersionRow{}
		for _, n := range numbers {
			out = append(out, store.VersionRow{Number: n})
		}
		return out
	}
	for _, c := range []struct {
		versions []string
		want     string
	}{
		{nil, "1.0"},
		{[]string{"3.2"}, "3.3"},
		{[]string{"1.1", "1.0", "1.3", "1.2"}, "1.4"}, // the highest, whatever the order
		{[]string{"2.0", "10.1"}, "10.2"},
		{[]string{"3"}, "3.1"},
		{[]string{"AWX #47"}, "AWX #47.1"},
		{[]string{"AWX #47.1", "AWX #47"}, "AWX #47.1.1"},
	} {
		if got := nextNumber(rows(c.versions...)); got != c.want {
			t.Errorf("nextNumber(%v) = %s, want %s", c.versions, got, c.want)
		}
	}
}

func TestPickedValues(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
		ok   bool
	}{
		{nil, "[]", true},
		{"", "[]", true},
		{"3", "[3]", true},
		{float64(999), "[999]", true},
		{[]any{"4", float64(777)}, "[4 777]", true},
		{map[string]any{"id": "4"}, "[]", false},
		{[]any{true}, "[]", false},
	} {
		got, ok := pickedValues(c.in)
		if s := strings.Join(got, " "); "["+s+"]" != c.want || ok != c.ok {
			t.Errorf("pickedValues(%v) = %v, %v", c.in, got, ok)
		}
	}
}

func TestApplyStates(t *testing.T) {
	value, failing := "x", "inputs.missing + 1"
	ui := api.UiSchema{Fields: map[string]api.UiField{
		"hidden": {}, "fixed": {}, "free": {}, "computed": {Rules: &api.UiRules{Value: &value}}, "broken": {Rules: &api.UiRules{Value: &failing}},
	}}
	msg := "value: no such key"
	states := api.FieldStates{
		"hidden":   {Visible: false, Required: true},
		"fixed":    {Visible: true, ReadOnly: true},
		"free":     {Visible: true, Required: true},
		"computed": {Visible: true, ReadOnly: true, Value: "web-orders"},
		"broken":   {Visible: true, ReadOnly: true, Error: &msg},
	}
	inputs := map[string]any{"hidden": "sneaked", "fixed": "gold", "free": "", "computed": "mine", "broken": "mine"}
	errs := applyStates(states, ui, map[string]any{"fixed": "bronze"}, inputs)
	if _, ok := inputs["hidden"]; ok || inputs["fixed"] != "bronze" || inputs["computed"] != "web-orders" {
		t.Fatalf("inputs = %v", inputs)
	}
	if _, ok := inputs["broken"]; !ok {
		t.Fatalf("a failed computed value dropped the input: %v", inputs)
	}
	fields := []string{}
	for _, e := range errs {
		fields = append(fields, e.Field+":"+deref(e.Code))
	}
	if strings.Join(fields, " ") != "/inputs/broken:rule_failed /inputs/free:required" {
		t.Fatalf("errors = %v", fields)
	}
	kept := hiddenErrors(states, []api.FieldError{fieldErr("/inputs/hidden", "required", "x"), fieldErr("/inputs/free", "required", "x")})
	if len(kept) != 1 || kept[0].Field != "/inputs/free" {
		t.Fatalf("hiddenErrors kept %v", kept)
	}
}
