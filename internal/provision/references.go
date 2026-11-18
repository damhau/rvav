package provision

import (
	"fmt"
	"math"
	"strings"

	"github.com/damhau/rvav/internal/policy"
)

// What a VM's tags and cloud-init read as it is built (ADR-0024): the VM
// itself, and what the components it reads built.

// selfOf is the VM being built, as a tags binding reads it (self).
func selfOf(in VMInput) map[string]any {
	return map[string]any{"index": in.VM.Index, "name": in.VM.Name, "fqdn": in.VM.FQDN, "address": in.IP, "component": in.VM.Component}
}

// buildTags works out a tags binding that reads self or components.
func buildTags(in VMInput) ([]string, error) {
	v, err := policy.EvalBuild(in.VM.TagsBinding, policy.Vars{"inputs": in.VM.TagsInputs, "self": selfOf(in),
		"components": integers(in.Components)})
	if err != nil {
		return nil, err
	}
	var list []any
	switch x := v.(type) {
	case string:
		list = []any{x}
	case []any:
		list = x
	default:
		return nil, fmt.Errorf("it gives %T, not a tag or a list of them", v)
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		t, _ := e.(string)
		if cat, tag, ok := strings.Cut(t, "/"); !ok || strings.TrimSpace(cat) == "" || strings.TrimSpace(tag) == "" || strings.Contains(tag, "/") {
			return nil, fmt.Errorf("%v is not a vSphere tag as category/tag", e)
		}
		out = append(out, t)
	}
	return out, nil
}

// integers gives back the whole numbers the workflow's history made
// floats (a count, an index), so CEL compares them with ints.
func integers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = integers(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = integers(e)
		}
		return out
	}
	return v
}
