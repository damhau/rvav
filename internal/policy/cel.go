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

var varNames = []string{"inputs", "environment", "project", "item", "vm", "user", "sizes"}

var (
	envOnce sync.Once
	env     *cel.Env
	envErr  error

	cacheMu sync.Mutex
	cache   = map[string]cel.Program{}
)

func celEnv() (*cel.Env, error) {
	envOnce.Do(func() {
		opts := make([]cel.EnvOption, 0, len(varNames))
		for _, n := range varNames {
			if n == "environment" {
				opts = append(opts, cel.Variable(n, cel.StringType))
				continue
			}
			opts = append(opts, cel.Variable(n, cel.DynType))
		}
		env, envErr = cel.NewEnv(opts...)
	})
	return env, envErr
}

func program(expr string) (cel.Program, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if p, ok := cache[expr]; ok {
		return p, nil
	}
	e, err := celEnv()
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
	cache[expr] = p
	return p, nil
}

// Check compiles expr without evaluating it, for validating stored policies.
func Check(expr string) error {
	_, err := program(expr)
	return err
}

// Eval evaluates expr and returns its value converted to a Go value.
func Eval(expr string, vars Vars) (any, error) {
	p, err := program(expr)
	if err != nil {
		return nil, err
	}
	act := map[string]any{}
	for _, n := range varNames {
		if v, ok := vars[n]; ok && v != nil {
			act[n] = v
		} else if n == "environment" {
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

// Bool evaluates expr and requires a boolean result.
func Bool(expr string, vars Vars) (bool, error) {
	v, err := Eval(expr, vars)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%q: want a boolean, got %T", expr, v)
	}
	return b, nil
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
