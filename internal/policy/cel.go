// Package policy evaluates the CEL expressions used by approval policies,
// form rules and blueprint bindings.
package policy

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// Vars are the values an expression can read. Missing keys are bound to
// empty maps so `inputs.size` on an empty form fails cleanly instead of panicking.
type Vars map[string]any

// set is a CEL environment with its variables and compiled programs.
type set struct {
	names   []string
	strings map[string]bool // variables that are strings (others are dynamic)

	once sync.Once
	env  *cel.Env
	err  error

	mu    sync.Mutex
	cache map[string]cel.Program
}

// forms is for approval policies, form rules and blueprint bindings.
var forms = &set{names: []string{"inputs", "environment", "project", "item", "vm", "user", "sizes"},
	strings: map[string]bool{"environment": true}}

// day2 is for day-2 policy conditions, where environment is an object
// (key, name, production).
var day2 = &set{names: []string{"action", "resource", "deployment", "project", "environment", "user", "inputs"}}

func (s *set) celEnv() (*cel.Env, error) {
	s.once.Do(func() {
		opts := make([]cel.EnvOption, 0, len(s.names))
		for _, n := range s.names {
			if s.strings[n] {
				opts = append(opts, cel.Variable(n, cel.StringType))
				continue
			}
			opts = append(opts, cel.Variable(n, cel.DynType))
		}
		s.env, s.err = cel.NewEnv(opts...)
	})
	return s.env, s.err
}

func (s *set) program(expr string) (cel.Program, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.cache[expr]; ok {
		return p, nil
	}
	e, err := s.celEnv()
	if err != nil {
		return nil, err
	}
	ast, iss := e.Compile(expr)
	if iss.Err() != nil {
		return nil, fmt.Errorf("compile %q: %w", expr, iss.Err())
	}
	p, err := e.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("program %q: %w", expr, err)
	}
	if s.cache == nil {
		s.cache = map[string]cel.Program{}
	}
	s.cache[expr] = p
	return p, nil
}

func (s *set) eval(expr string, vars Vars) (any, error) {
	p, err := s.program(expr)
	if err != nil {
		return nil, err
	}
	act := map[string]any{}
	for _, n := range s.names {
		if v, ok := vars[n]; ok && v != nil {
			act[n] = v
		} else if s.strings[n] {
			act[n] = ""
		} else {
			act[n] = map[string]any{}
		}
	}
	out, _, err := p.Eval(act)
	if err != nil {
		return nil, err
	}
	return native(out), nil
}

func (s *set) boolean(expr string, vars Vars) (bool, error) {
	v, err := s.eval(expr, vars)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%q: want a boolean, got %T", expr, v)
	}
	return b, nil
}

// Check compiles expr without evaluating it, for validating stored policies.
func Check(expr string) error {
	_, err := forms.program(expr)
	return err
}

// Eval evaluates expr and returns its value converted to a Go value.
func Eval(expr string, vars Vars) (any, error) { return forms.eval(expr, vars) }

// Bool evaluates expr and requires a boolean result.
func Bool(expr string, vars Vars) (bool, error) { return forms.boolean(expr, vars) }

// Day2Bool evaluates the condition of a day-2 policy.
func Day2Bool(expr string, vars Vars) (bool, error) { return day2.boolean(expr, vars) }

// CheckDay2 compiles a day-2 policy condition without evaluating it.
func CheckDay2(expr string) error {
	_, err := day2.program(expr)
	return err
}

func native(v ref.Val) any {
	switch v.Type() {
	case types.BoolType, types.StringType, types.IntType, types.UintType, types.DoubleType:
		return v.Value()
	case types.ListType:
		l := v.(interface {
			Size() ref.Val
			Get(ref.Val) ref.Val
		})
		n := int(l.Size().(types.Int))
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = native(l.Get(types.Int(i)))
		}
		return out
	}
	return v.Value()
}
