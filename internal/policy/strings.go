package policy

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/functions"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/ext"
	"github.com/google/cel-go/interpreter"
)

// cel-go charges the string extensions one unit whatever they build, so
// nested replace calls grow a string tenfold per call while the cost limit
// sees a handful of units: a few calls exhaust the API's memory. Every
// function that builds a string or a list is refused past these sizes, and
// charged for what it built.
const (
	maxStringBytes = 1 << 20
	maxListItems   = 100_000
)

// estimates size what a call would build before it builds it, for the
// functions whose result can outgrow their arguments by more than a constant
// factor. The other functions are checked on their result.
var estimates = map[string]func(args []ref.Val) int{
	"string_replace_string_string":     func(a []ref.Val) int { return replaceSize(a[0], a[1], a[2], -1) },
	"string_replace_string_string_int": func(a []ref.Val) int { return replaceSize(a[0], a[1], a[2], int64(a[3].(types.Int))) },
	"string_split_string":              func(a []ref.Val) int { return splitItems(a[0], a[1], -1) },
	"string_split_string_int":          func(a []ref.Val) int { return splitItems(a[0], a[1], int64(a[2].(types.Int))) },
	"list_join":                        func(a []ref.Val) int { return joinSize(a[0], "") },
	"list_join_string":                 func(a []ref.Val) int { return joinSize(a[0], string(a[1].(types.String))) },
	"string_format": func(a []ref.Val) int {
		f := len(a[0].(types.String))
		return f + valueSize(a[1], maxStringBytes-f)
	},
}

// building is every overload of the string extensions that returns a new
// string or list.
var building = []string{
	"string_char_at_int", "string_lower_ascii", "string_upper_ascii", "string_trim", "string_reverse",
	"string_substring_int", "string_substring_int_int", "strings_quote", "string_format",
	"string_replace_string_string", "string_replace_string_string_int",
	"string_split_string", "string_split_string_int", "list_join", "list_join_string",
}

// boundedStrings is CEL's string extensions, version 4, with every function
// that builds a string or a list refused past maxStringBytes or maxListItems.
func boundedStrings() ([]cel.EnvOption, error) {
	lib := ext.Strings(ext.StringsVersion(4))
	base, err := cel.NewEnv(lib)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, id := range building {
		wanted[id] = true
	}
	opts := []cel.EnvOption{lib}
	for name, fn := range base.Functions() {
		binds, err := fn.Bindings()
		if err != nil {
			return nil, err
		}
		calls := map[string]*functions.Overload{}
		for _, b := range binds {
			calls[b.Operator] = b
		}
		for _, o := range fn.OverloadDecls() {
			if !wanted[o.ID()] {
				continue
			}
			delete(wanted, o.ID())
			bind := cel.FunctionBinding(bounded(name, calls[o.ID()], estimates[o.ID()]))
			if o.IsMemberFunction() {
				opts = append(opts, cel.Function(name, cel.MemberOverload(o.ID(), o.ArgTypes(), o.ResultType(), bind)))
			} else {
				opts = append(opts, cel.Function(name, cel.Overload(o.ID(), o.ArgTypes(), o.ResultType(), bind)))
			}
		}
	}
	if len(wanted) > 0 {
		return nil, fmt.Errorf("string extensions: missing overloads %v", wanted)
	}
	return opts, nil
}

// builtCosts charges each building function for its result: a tenth of a
// unit per byte, as cel-go charges string traversals, and a unit per item.
func builtCosts() cel.ProgramOption {
	var opts []interpreter.CostTrackerOption
	for _, id := range building {
		opts = append(opts, interpreter.OverloadCostTracker(id, func(_ []ref.Val, result ref.Val) *uint64 {
			c := uint64(1)
			switch r := result.(type) {
			case types.String:
				c += uint64(math.Ceil(float64(len(r)) * common.StringTraversalCostFactor))
			case traits.Lister:
				c += uint64(r.Size().(types.Int))
			}
			return &c
		}))
	}
	return cel.CostTrackerOptions(opts...)
}

func bounded(name string, o *functions.Overload, estimate func([]ref.Val) int) functions.FunctionOp {
	return func(args ...ref.Val) ref.Val {
		for _, a := range args {
			if types.IsUnknownOrError(a) {
				return a
			}
		}
		if estimate != nil {
			if err := tooBig(name, name == "split", estimate(args)); err != nil {
				return err
			}
		}
		out := call(o, args)
		switch r := out.(type) {
		case types.String:
			if err := tooBig(name, false, len(r)); err != nil {
				return err
			}
		case traits.Lister:
			if err := tooBig(name, true, int(r.Size().(types.Int))); err != nil {
				return err
			}
		}
		return out
	}
}

func call(o *functions.Overload, args []ref.Val) ref.Val {
	switch {
	case len(args) == 1 && o.Unary != nil:
		return o.Unary(args[0])
	case len(args) == 2 && o.Binary != nil:
		return o.Binary(args[0], args[1])
	case o.Function != nil:
		return o.Function(args...)
	}
	return types.NewErr("no implementation for %s", o.Operator)
}

func tooBig(name string, list bool, size int) ref.Val {
	if list && size > maxListItems {
		return types.NewErr("%s would build a list of more than %d items", name, maxListItems)
	}
	if !list && size > maxStringBytes {
		return types.NewErr("%s would build a string of more than %d bytes", name, maxStringBytes)
	}
	return nil
}

func replaceSize(str, old, new ref.Val, n int64) int {
	s, o, w := string(str.(types.String)), string(old.(types.String)), string(new.(types.String))
	count := strings.Count(s, o)
	if o == "" {
		count = utf8.RuneCountInString(s) + 1
	}
	if n >= 0 && int64(count) > n {
		count = int(n)
	}
	return len(s) + count*(len(w)-len(o))
}

func splitItems(str, sep ref.Val, n int64) int {
	s, p := string(str.(types.String)), string(sep.(types.String))
	count := strings.Count(s, p) + 1
	if p == "" {
		count = utf8.RuneCountInString(s)
	}
	if n >= 0 && int64(count) > n {
		count = int(n)
	}
	return count
}

func joinSize(list ref.Val, sep string) int {
	l, ok := list.(traits.Lister)
	if !ok {
		return 0
	}
	size := 0
	n := l.Size().(types.Int)
	for i := types.Int(0); i < n && size <= maxStringBytes; i++ {
		if i > 0 {
			size += len(sep)
		}
		if s, ok := l.Get(i).(types.String); ok {
			size += len(s)
		}
	}
	return size
}

// valueSize is about how many bytes v takes once formatted, counted only
// up to budget so a list holding the same large list many times stops early.
func valueSize(v ref.Val, budget int) int {
	switch x := v.(type) {
	case types.String:
		return len(x)
	case types.Bytes:
		return len(x)
	case traits.Lister:
		size := 2
		n := x.Size().(types.Int)
		for i := types.Int(0); i < n && size <= budget; i++ {
			size += 2 + valueSize(x.Get(i), budget-size)
		}
		return size
	case traits.Mapper:
		size := 2
		for it := x.Iterator(); it.HasNext() == types.True && size <= budget; {
			k := it.Next()
			size += 4 + valueSize(k, budget-size)
			size += valueSize(x.Get(k), budget-size)
		}
		return size
	}
	return 24
}
