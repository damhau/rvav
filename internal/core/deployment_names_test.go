package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/day2"
)

func TestNamesRvavMakesFit(t *testing.T) {
	long := strings.Repeat("é", 79) + " x"
	cases := []struct{ name, suffix, want string }{
		{"Billing", "", "Billing"},
		{"Billing", "2", "Billing 2"},
		{strings.Repeat("a", 80), "", strings.Repeat("a", 80)},
		{strings.Repeat("a", 80), "REQ-10002", strings.Repeat("a", 70) + " REQ-10002"},
		{long, "", strings.Repeat("é", 79)},
		{strings.Repeat("b", 75) + "    c", "12", strings.Repeat("b", 75) + " 12"},
	}
	for _, c := range cases {
		got := withSuffix(c.name, c.suffix)
		if got != c.want || utf8.RuneCountInString(got) > day2.MaxDeploymentName {
			t.Errorf("withSuffix(%q, %q) = %q, want %q", c.name, c.suffix, got, c.want)
		}
	}
	for _, n := range numbered(strings.Repeat("c", 80), 12) {
		if utf8.RuneCountInString(n) > day2.MaxDeploymentName {
			t.Errorf("numbered name %q is too long", n)
		}
	}
}
