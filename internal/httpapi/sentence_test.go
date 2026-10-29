package httpapi

import "testing"

func TestSentence(t *testing.T) {
	for in, want := range map[string]string{
		"you can't approve your own request": "You can't approve your own request.",
		"Only the owner can delete it.":      "Only the owner can delete it.",
	} {
		if got := sentence(in); got != want {
			t.Errorf("sentence(%q) = %q", in, got)
		}
	}
}
