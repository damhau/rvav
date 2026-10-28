package httpapi

import "testing"

// A value a spreadsheet would read as a formula is exported as text.
func TestCSVCell(t *testing.T) {
	for in, want := range map[string]string{
		`=HYPERLINK("https://x","open")`: `'=HYPERLINK("https://x","open")`,
		"+1":                             "'+1", "-1": "'-1", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx",
		"Marc Keller": "Marc Keller", "": "", "dvpg-app-dev-vl310": "dvpg-app-dev-vl310",
	} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q) = %q, want %q", in, got, want)
		}
	}
}
