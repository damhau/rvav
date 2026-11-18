package policy

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// String functions that build large strings or lists fail before building
// them, and what they build counts against the cost limit (T1.7).
func TestStringFunctionsAreBounded(t *testing.T) {
	big := strings.Repeat("a", 200_000)
	list := make([]any, 1000)
	for i := range list {
		list[i] = i
	}
	vars := Vars{"inputs": map[string]any{"big": big, "l": list, "slash": `\`}}
	tenfold := strings.Repeat(".replace('a','aaaaaaaaaa')", 9)
	for expr, want := range map[string]string{
		`'aaaaaaaaaa'` + tenfold + `.size()`:                                                        "would build a string of more than",
		`inputs.big.replace('a', inputs.big).size()`:                                                "would build a string of more than",
		`inputs.l.map(i, inputs.big).join(',').size()`:                                              "would build a string of more than",
		`'%s'.format([inputs.l.map(i, inputs.big)]).size()`:                                         "would build a string of more than",
		`(inputs.big + inputs.big + inputs.big).split('').size()`:                                   "would build a list of more than",
		`inputs.l.map(i, inputs.big.upperAscii()).size()`:                                           "cost limit",
		strings.Repeat("strings.quote(", 25) + "inputs.slash" + strings.Repeat(")", 25) + ".size()": "would build a string of more than",
	} {
		start := time.Now()
		_, err := Eval(expr, vars)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.60s…: %v, want %q", expr, err, want)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%.60s… took %s", expr, d)
		}
	}
	for expr, want := range map[string]any{
		`inputs.big.replace('a', 'b').size()`:                int64(200_000),
		`inputs.big.substring(0, 3).upperAscii()`:            "AAA",
		`inputs.l.map(i, 'x').join('').size()`:               int64(1000),
		`'%s-%d'.format(['a', 7])`:                           "a-7",
		`'a,b,c'.split(',', 2)`:                              []any{"a", "b,c"},
		`'aaaaaaaaaa'.replace('a','aaaaaaaaaa', 1).size()`:   int64(19),
		`inputs.big.substring(0, 50000).split('').size()`:    int64(50_000),
		`strings.quote(inputs.slash)`:                        `"\\"`,
		`['x', 'y'].join(inputs.big).size()`:                 int64(200_002),
		`inputs.big.replace('', '').size()`:                  int64(200_000),
		`inputs.big.lowerAscii().reverse().trim().charAt(0)`: "a",
	} {
		got, err := Eval(expr, vars)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if g, w := fmt.Sprint(got), fmt.Sprint(want); g != w {
			t.Errorf("%s = %s, want %s", expr, g, w)
		}
	}
}
