package provision

import (
	"strings"

	"github.com/damhau/rvav/internal/policy"
)

// A composite step's CEL, whose errors a requester reads on their request: a
// step's and the request's error, the timeline. cel-go's errors quote what they
// met, a key a map lacks, a pattern's text, a compile error the whole
// expression, which may be the literals a requester's view masks (ADR-0033). So
// a step evaluates through stepEval, which says why in rvav's words, and the
// place that fails names the expression with its literals masked.

// said is an error in rvav's own words, which a requester may read as it is.
type said string

func (s said) Error() string { return string(s) }

const (
	errNotCEL   = said("it isn't valid CEL. Ask a catalog admin to check this item.")
	errMissing  = said("what it reads isn't there")
	errUnread   = said("it can't be read")
	errNotTruth = said("it isn't true or false")
)

// stepEval evaluates a step's expression. Its error is one of the above.
func stepEval(expr string, vars policy.Vars) (any, error) {
	if policy.CheckComposite(expr) != nil {
		return nil, errNotCEL
	}
	v, err := policy.CompositeEval(expr, vars)
	switch {
	case err == nil:
		return v, nil
	case strings.HasPrefix(err.Error(), "no such key"):
		return nil, errMissing
	}
	return nil, errUnread
}

// stepBool evaluates a step's condition: true or false, or one of the errors
// above.
func stepBool(expr string, vars policy.Vars) (bool, error) {
	v, err := stepEval(expr, vars)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, errNotTruth
	}
	return b, nil
}
