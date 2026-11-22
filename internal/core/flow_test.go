package core

import (
	"encoding/json"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
)

// A wait refused on a catch path and in the error handler is said of each
// where it is.
func TestCatchRefusedSaysWhere(t *testing.T) {
	b, err := blueprint.Parse(json.RawMessage(`{"kind": "composite", "steps": [
		{"key": "make", "rest": {"method": "POST", "url": "https://h.example/x", "body": "{}"},
		 "catch": [{"key": "later", "signal": {"name": "a", "timeout": "1h"}}]}],
		"on_error": [{"key": "after", "signal": {"name": "b", "timeout": "1h"}},
		 {"key": "loop", "rest": {"method": "POST", "url": "https://h.example/x", "body": "{}"}, "for_each": {"items": "[1]"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range checkComposite(b, "/definition/blueprint", false) {
		got[e.Field] = e.Message
	}
	want := map[string]string{
		"/definition/blueprint/steps/0/catch/0": "A catch path doesn't wait: it runs while the request is failing. Use a message, a REST call, an AWX job or a custom workflow.",
		"/definition/blueprint/on_error/0":      "The error handler doesn't wait: it runs while the request is failing. Use a message, a REST call, an AWX job or a custom workflow.",
		"/definition/blueprint/on_error/1":      "A step of the error handler doesn't loop.",
	}
	for field, msg := range want {
		if got[field] != msg {
			t.Errorf("%s: %q, want %q", field, got[field], msg)
		}
	}
	if len(got) != len(want) {
		t.Errorf("problems = %v", got)
	}
}
