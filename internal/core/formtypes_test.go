package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// fieldProblems validates one input against one property's schema and
// returns the problems' codes and messages, joined.
func fieldProblems(t *testing.T, prop map[string]any, value any) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"f": prop}})
	s, err := compileSchemaUncached(b)
	if err != nil {
		t.Fatalf("compile %v: %v", prop, err)
	}
	in, _ := json.Marshal(map[string]any{"f": value})
	errs, err := validateWith(s, in)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range errs {
		out = append(out, e.Field+" "+*e.Code+": "+e.Message)
	}
	return strings.Join(out, "; ")
}

func TestDateAndTimeInputs(t *testing.T) {
	when := map[string]any{"type": "string", "format": "date-time",
		"formatMinimum": "2026-12-01T00:00:00+01:00", "formatMaximum": "2026-12-31T23:59:59+01:00"}
	day := map[string]any{"type": "string", "format": "date", "formatMinimum": "2026-12-01"}
	for _, c := range []struct {
		prop  map[string]any
		value any
		want  string
	}{
		{when, "2026-12-10T14:30:00+01:00", ""},
		{when, "2026-12-10T13:30:00Z", ""},
		{when, "2026-12-10T14:30:00", "/inputs/f format: Enter a date and time with its time zone"},
		{when, "10.12.2026 14:30", "/inputs/f format:"},
		// The same instant as the bound, written in another zone.
		{when, "2026-11-30T23:00:00Z", ""},
		{when, "2026-11-30T22:59:59Z", "/inputs/f out_of_range: Pick a time on or after 2026-12-01T00:00:00+01:00."},
		{when, "2027-01-01T00:00:00+01:00", "/inputs/f out_of_range: Pick a time on or before 2026-12-31T23:59:59+01:00."},
		{day, "2026-12-01", ""},
		{day, "2026-11-30", "/inputs/f out_of_range: Pick a date on or after 2026-12-01."},
		{day, "2026-02-30", "/inputs/f format: Enter a date as YYYY-MM-DD"},
	} {
		if got := fieldProblems(t, c.prop, c.value); c.want == "" && got != "" || !strings.HasPrefix(got, c.want) {
			t.Errorf("%v against %v: %q, want %q", c.value, c.prop, got, c.want)
		}
	}
	for _, bad := range []map[string]any{
		{"type": "string", "format": "date-time", "formatMinimum": "tomorrow"},
		{"type": "string", "formatMinimum": "2026-12-01"},
		{"type": "string", "format": "date", "formatMaximum": 3},
	} {
		b, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"f": bad}})
		if _, err := compileSchemaUncached(b); err == nil {
			t.Errorf("%v compiled", bad)
		}
	}
}

func TestDecimalInputs(t *testing.T) {
	ratio := map[string]any{"type": "number", "minimum": 0.5, "maximum": 4, "multipleOf": 0.25}
	for value, want := range map[any]string{
		1.75: "",
		0.5:  "",
		0.25: "/inputs/f minimum: Enter 0.5 or more.",
		4.5:  "/inputs/f maximum: Enter 4 or less.",
		1.3:  "/inputs/f multiple_of: Enter a multiple of 0.25.",
		"2":  "/inputs/f type: Enter a number.",
	} {
		if got := fieldProblems(t, ratio, value); want == "" && got != "" || !strings.HasPrefix(got, want) {
			t.Errorf("%v: %q, want %q", value, got, want)
		}
	}
}
