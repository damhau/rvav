package provision

import (
	"context"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/policy"
)

// A requester reads a step's error. cel-go's errors quote what they met, a key
// a map lacks or a pattern's text, so none reaches them: each place a step
// evaluates CEL says why in rvav's words, its expression's literals masked.
func TestStepExpressionErrors(t *testing.T) {
	const secret = "tok_live_SECRET"
	vars := policy.Vars{"inputs": map[string]any{"name": "web"},
		"steps": map[string]any{"x": map[string]any{"json": map[string]any{"a": float64(1)}}}}
	sites := map[string]func(expr string) error{
		"a branch's case": func(expr string) error {
			_, err := chooseSide([]BranchChoice{{When: expr}}, vars)
			return err
		},
		"a step's when": func(expr string) error {
			_, err := resolveStep(ResolveInput{When: expr}, vars)
			return err
		},
		"a step's input": func(expr string) error {
			_, err := resolveStep(ResolveInput{With: map[string]any{"size": expr}}, vars)
			return err
		},
		"a message": func(expr string) error {
			_, err := fillMessage("Hello ${"+expr+"}.", vars)
			return err
		},
		"a URL": func(expr string) error {
			_, _, err := restTarget(context.Background(), nil, "https://cmdb.example/ci/${"+expr+"}", vars)
			return err
		},
		"a body": func(expr string) error {
			_, err := (&Activities{}).restBody(context.Background(), "req", expr, vars)
			return err
		},
		"a poll's condition": func(expr string) error {
			_, err := pollHolds(expr, vars)
			return err
		},
	}
	for expr, why := range map[string]string{
		"steps.x.json['" + secret + "']":         "what it reads isn't there",
		"inputs.name.matches('" + secret + "(')": "it can't be read",
		"int('" + secret + "')":                  "it can't be read",
		"steps.x.json['a'] + '" + secret + "'":   "it can't be read",
	} {
		for site, run := range sites {
			err := run(expr)
			if err == nil {
				t.Errorf("%s ${%s} didn't fail", site, expr)
				continue
			}
			msg := errMessage(err)
			if strings.Contains(msg, secret) {
				t.Errorf("%s ${%s} reads its literal: %s", site, expr, msg)
			}
			if site != "a poll's condition" && !strings.Contains(msg, why) {
				t.Errorf("%s ${%s} = %q, want %q", site, expr, msg, why)
			}
		}
	}
	// A condition that isn't true or false says so, and quotes nothing either.
	for _, site := range []string{"a branch's case", "a step's when", "a poll's condition"} {
		msg := errMessage(sites[site]("'" + secret + "'"))
		if strings.Contains(msg, secret) || !strings.Contains(msg, "true or false") {
			t.Errorf("%s: %q", site, msg)
		}
	}
}
