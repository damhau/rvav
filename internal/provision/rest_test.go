package provision

import (
	"errors"
	"reflect"
	"testing"

	"go.temporal.io/sdk/temporal"
)

func TestAccepted(t *testing.T) {
	for _, tc := range []struct {
		accept []int
		status int
		want   bool
	}{
		{nil, 200, true},
		{nil, 299, true},
		{nil, 300, false},
		{nil, 199, false},
		{nil, 404, false},
		{[]int{200, 404}, 404, true},
		{[]int{200, 404}, 201, false},
	} {
		if got := accepted(tc.accept, tc.status); got != tc.want {
			t.Errorf("accepted(%v, %d) = %v", tc.accept, tc.status, got)
		}
	}
}

// What a step keeps: a path that names one thing gives it or null; one that
// can match several gives a list, however many it matches.
func TestKeepOfAnswer(t *testing.T) {
	const answer = `{"id": 42, "result": {"name": "a"}, "items": [{"n": 1}, {"n": 2}]}`
	for _, tc := range []struct {
		raw, path string
		want      any
	}{
		{answer, "$.id", float64(42)},
		{answer, "$.result.name", "a"},
		{answer, "$['result']['name']", "a"},
		{answer, "$.items[1].n", float64(2)},
		{answer, "$.missing", nil},
		{answer, "$.items[*].n", []any{float64(1), float64(2)}},
		{answer, "$.items[?(@.n == 2)].n", []any{float64(2)}},
		{answer, "$.result[*].missing", []any{}},
		{answer, "$..n", []any{float64(1), float64(2)}},
		{`[1, 2]`, "", []any{float64(1), float64(2)}},
		{"not JSON", "", nil},
		{"", "$.id", nil},
	} {
		got, err := keepOfAnswer([]byte(tc.raw), tc.path)
		if err != nil {
			t.Errorf("%s of %q: %v", tc.path, tc.raw, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s of %q = %#v, want %#v", tc.path, tc.raw, got, tc.want)
		}
	}
	if _, err := keepOfAnswer([]byte(answer), "$["); err == nil {
		t.Error("a path that doesn't parse was accepted")
	}
}

// A failure is tried again only if it might do better and the call can be
// repeated: never a POST or a PATCH, whose effect is unknown.
func TestRestFailure(t *testing.T) {
	for _, tc := range []struct {
		method string
		again  bool
		want   bool
	}{
		{"GET", true, true},
		{"PUT", true, true},
		{"DELETE", true, true},
		{"GET", false, false},
		{"POST", true, false},
		{"PATCH", true, false},
	} {
		var app *temporal.ApplicationError
		err := restFailure(tc.method, tc.again, "it failed")
		if !errors.As(err, &app) {
			t.Fatalf("%s: %T", tc.method, err)
		}
		if retried := !app.NonRetryable(); retried != tc.want || err.Error() == "" {
			t.Errorf("%s (again %v): retried = %v, want %v", tc.method, tc.again, retried, tc.want)
		}
	}
}
