package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// field is what converting between Terraform's values and the API's JSON
// needs of an attribute. The attribute's name is its JSON name.
type field struct {
	typ tftypes.Type
	// computed: rvav's value wins (computed, or optional and computed);
	// else an empty value from rvav means "not set" when the
	// configuration doesn't set it.
	computed bool
	// readOnly: computed only, never sent.
	readOnly bool
	// json: a string of JSON, which the API has as an object.
	json  bool
	attrs map[string]*field
	elem  *field
}

type anyAttribute interface {
	GetType() attr.Type
	IsComputed() bool
	IsOptional() bool
	IsRequired() bool
}

func leaf(a anyAttribute) *field {
	f := &field{typ: a.GetType().TerraformType(context.Background()), computed: a.IsComputed()}
	f.readOnly = f.computed && !a.IsOptional() && !a.IsRequired()
	return f
}

// resourceFields are a resource schema's attributes as fields.
func resourceFields(attrs map[string]schema.Attribute) *field {
	root := &field{attrs: map[string]*field{}}
	types := map[string]tftypes.Type{}
	for name, a := range attrs {
		f := resourceField(a)
		root.attrs[name] = f
		types[name] = f.typ
	}
	root.typ = tftypes.Object{AttributeTypes: types}
	return root
}

func resourceField(a schema.Attribute) *field {
	f := leaf(a)
	switch x := a.(type) {
	case schema.StringAttribute:
		_, f.json = x.CustomType.(jsontypes.NormalizedType)
	case schema.SetAttribute, schema.ListAttribute, schema.MapAttribute:
		f.elem = elemField(f.typ)
	case schema.ListNestedAttribute:
		f.elem = resourceFields(x.NestedObject.Attributes)
	case schema.SingleNestedAttribute:
		f.attrs = resourceFields(x.Attributes).attrs
	}
	return f
}

// dataFields are a data source schema's attributes as fields.
func dataFields(attrs map[string]dschema.Attribute) *field {
	root := &field{attrs: map[string]*field{}}
	types := map[string]tftypes.Type{}
	for name, a := range attrs {
		f := leaf(a)
		switch x := a.(type) {
		case dschema.StringAttribute:
			_, f.json = x.CustomType.(jsontypes.NormalizedType)
		case dschema.SetAttribute, dschema.ListAttribute, dschema.MapAttribute:
			f.elem = elemField(f.typ)
		case dschema.ListNestedAttribute:
			f.elem = dataFields(x.NestedObject.Attributes)
			f.elem.computed = f.computed
		}
		root.attrs[name] = f
		types[name] = f.typ
	}
	root.typ = tftypes.Object{AttributeTypes: types}
	return root
}

func elemField(t tftypes.Type) *field {
	switch c := t.(type) {
	case tftypes.Set:
		return &field{typ: c.ElementType}
	case tftypes.List:
		return &field{typ: c.ElementType}
	case tftypes.Map:
		return &field{typ: c.ElementType}
	}
	return nil
}

// emptyJSON is a value rvav gives for "not set".
func emptyJSON(j any) bool {
	switch x := j.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func emptyValue(v tftypes.Value) bool {
	if _, obj := v.Type().(tftypes.Object); obj || !v.IsKnown() || v.IsNull() {
		return false
	}
	j, err := toJSON(&field{typ: v.Type()}, v)
	return err == nil && emptyJSON(j)
}

// fromJSON is rvav's value j as the attribute's, given its value before
// (the plan, or the state): what the configuration left out stays null
// when rvav has it empty, and an unknown value takes rvav's.
func fromJSON(f *field, j any, prior tftypes.Value) (tftypes.Value, error) {
	if !f.computed && prior.IsKnown() && emptyJSON(j) {
		if prior.IsNull() {
			return tftypes.NewValue(f.typ, nil), nil
		}
		if emptyValue(prior) {
			return prior, nil
		}
	}
	if j == nil {
		return tftypes.NewValue(f.typ, nil), nil
	}
	if f.json {
		b, err := json.Marshal(j)
		if err != nil {
			return tftypes.Value{}, err
		}
		return tftypes.NewValue(tftypes.String, string(b)), nil
	}
	switch t := f.typ.(type) {
	case tftypes.Object:
		obj, ok := j.(map[string]any)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("rvav gave %T where an object was expected", j)
		}
		priors := children(prior, t)
		vals := map[string]tftypes.Value{}
		allNull := true
		for name, sub := range f.attrs {
			v, err := fromJSON(sub, obj[name], priors[name])
			if err != nil {
				return tftypes.Value{}, fmt.Errorf("%s: %w", name, err)
			}
			vals[name] = v
			allNull = allNull && v.IsNull()
		}
		if allNull && !f.computed && prior.IsKnown() && prior.IsNull() {
			return tftypes.NewValue(f.typ, nil), nil
		}
		return tftypes.NewValue(f.typ, vals), nil
	case tftypes.List, tftypes.Set:
		arr, ok := j.([]any)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("rvav gave %T where a list was expected", j)
		}
		var priors []tftypes.Value
		if _, isList := t.(tftypes.List); isList && prior.IsKnown() && !prior.IsNull() {
			_ = prior.As(&priors)
			arr = alignTo(f.elem, arr, priors)
		}
		vals := make([]tftypes.Value, 0, len(arr))
		for i, e := range arr {
			p := tftypes.NewValue(f.elem.typ, nil)
			if !prior.IsKnown() {
				p = tftypes.NewValue(f.elem.typ, tftypes.UnknownValue)
			} else if i < len(priors) {
				p = priors[i]
			}
			v, err := fromJSON(f.elem, e, p)
			if err != nil {
				return tftypes.Value{}, err
			}
			vals = append(vals, v)
		}
		return tftypes.NewValue(f.typ, vals), nil
	case tftypes.Map:
		obj, ok := j.(map[string]any)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("rvav gave %T where a map was expected", j)
		}
		vals := map[string]tftypes.Value{}
		for k, e := range obj {
			v, err := fromJSON(f.elem, e, tftypes.NewValue(f.elem.typ, tftypes.UnknownValue))
			if err != nil {
				return tftypes.Value{}, err
			}
			vals[k] = v
		}
		return tftypes.NewValue(f.typ, vals), nil
	}
	return primitive(f.typ, j)
}

func children(prior tftypes.Value, t tftypes.Object) map[string]tftypes.Value {
	out := map[string]tftypes.Value{}
	known := prior.IsKnown()
	if known && !prior.IsNull() {
		_ = prior.As(&out)
		return out
	}
	for name, at := range t.AttributeTypes {
		if known {
			out[name] = tftypes.NewValue(at, nil)
		} else {
			out[name] = tftypes.NewValue(at, tftypes.UnknownValue)
		}
	}
	return out
}

// alignTo puts rvav's list in the order of the one before, where its
// elements are the same, so that rvav ordering a list its own way isn't a
// change. Elements rvav has besides go last, in its order.
func alignTo(elem *field, arr []any, priors []tftypes.Value) []any {
	used := make([]bool, len(arr))
	out := make([]any, 0, len(arr))
	for _, p := range priors {
		for i, e := range arr {
			if used[i] {
				continue
			}
			if v, err := fromJSON(elem, e, p); err == nil && v.Equal(p) {
				used[i] = true
				out = append(out, e)
				break
			}
		}
	}
	for i, e := range arr {
		if !used[i] {
			out = append(out, e)
		}
	}
	return out
}

func primitive(t tftypes.Type, j any) (tftypes.Value, error) {
	switch {
	case t.Is(tftypes.String):
		s, ok := j.(string)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("rvav gave %T where a string was expected", j)
		}
		return tftypes.NewValue(t, s), nil
	case t.Is(tftypes.Bool):
		b, ok := j.(bool)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("rvav gave %T where a boolean was expected", j)
		}
		return tftypes.NewValue(t, b), nil
	case t.Is(tftypes.Number):
		var n *big.Float
		switch x := j.(type) {
		case json.Number:
			f, _, err := big.ParseFloat(string(x), 10, 512, big.ToNearestEven)
			if err != nil {
				return tftypes.Value{}, err
			}
			n = f
		case float64:
			n = big.NewFloat(x)
		case int:
			n = new(big.Float).SetInt64(int64(x))
		case int64:
			n = new(big.Float).SetInt64(x)
		default:
			return tftypes.Value{}, fmt.Errorf("rvav gave %T where a number was expected", j)
		}
		return tftypes.NewValue(t, n), nil
	}
	return tftypes.Value{}, fmt.Errorf("unsupported type %s", t)
}

// toJSON is a known, non-null value as the API takes it.
func toJSON(f *field, v tftypes.Value) (any, error) {
	if f.json {
		var s string
		if err := v.As(&s); err != nil {
			return nil, err
		}
		var j any
		if err := json.Unmarshal([]byte(s), &j); err != nil {
			return nil, fmt.Errorf("not JSON: %w", err)
		}
		return j, nil
	}
	switch t := v.Type().(type) {
	case tftypes.Object:
		return bodyOf(f, v, false)
	case tftypes.List, tftypes.Set:
		var elems []tftypes.Value
		if err := v.As(&elems); err != nil {
			return nil, err
		}
		elem := f.elem
		if elem == nil {
			elem = elemField(t)
		}
		out := make([]any, 0, len(elems))
		for _, e := range elems {
			if !e.IsKnown() || e.IsNull() {
				continue
			}
			j, err := toJSON(elem, e)
			if err != nil {
				return nil, err
			}
			out = append(out, j)
		}
		return out, nil
	case tftypes.Map:
		var elems map[string]tftypes.Value
		if err := v.As(&elems); err != nil {
			return nil, err
		}
		elem := f.elem
		if elem == nil {
			elem = elemField(t)
		}
		out := map[string]any{}
		for k, e := range elems {
			if !e.IsKnown() || e.IsNull() {
				continue
			}
			j, err := toJSON(elem, e)
			if err != nil {
				return nil, err
			}
			out[k] = j
		}
		return out, nil
	}
	switch {
	case v.Type().Is(tftypes.String):
		var s string
		err := v.As(&s)
		return s, err
	case v.Type().Is(tftypes.Bool):
		var b bool
		err := v.As(&b)
		return b, err
	case v.Type().Is(tftypes.Number):
		var n big.Float
		if err := v.As(&n); err != nil {
			return nil, err
		}
		if n.IsInt() {
			i, _ := n.Int64()
			return i, nil
		}
		x, _ := n.Float64()
		return x, nil
	}
	return nil, fmt.Errorf("unsupported type %s", v.Type())
}

// bodyOf is an object's attributes the user sets, as the API takes them:
// unknown and null ones left out, or with zero, null ones sent empty, so
// that a PATCH clears what the configuration no longer sets.
func bodyOf(f *field, v tftypes.Value, zero bool) (map[string]any, error) {
	var vals map[string]tftypes.Value
	if err := v.As(&vals); err != nil {
		return nil, err
	}
	out := map[string]any{}
	names := make([]string, 0, len(f.attrs))
	for name := range f.attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sub := f.attrs[name]
		val, ok := vals[name]
		if sub.readOnly || !ok || !val.IsKnown() {
			continue
		}
		if val.IsNull() {
			if z, ok := zeroOf(sub); zero && ok {
				out[name] = z
			}
			continue
		}
		j, err := toJSON(sub, val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = j
	}
	return out, nil
}

func zeroOf(f *field) (any, bool) {
	switch {
	case f.json:
		return nil, false
	case f.typ.Is(tftypes.String):
		return "", true
	case f.typ.Is(tftypes.Bool):
		return false, true
	}
	switch f.typ.(type) {
	case tftypes.List, tftypes.Set:
		return []any{}, true
	case tftypes.Map:
		return map[string]any{}, true
	}
	return nil, false
}

// stateJSON is a whole state as JSON, computed attributes included, for
// the code that reads or changes a resource.
func stateJSON(f *field, v tftypes.Value) map[string]any {
	if !v.IsKnown() || v.IsNull() {
		return map[string]any{}
	}
	out, err := bodyOf(everything(f), v, false)
	if err != nil {
		return map[string]any{}
	}
	return out
}

// everything is f with no attribute read-only.
func everything(f *field) *field {
	if f == nil {
		return nil
	}
	c := *f
	c.readOnly = false
	c.elem = everything(f.elem)
	if f.attrs != nil {
		c.attrs = map[string]*field{}
		for k, a := range f.attrs {
			c.attrs[k] = everything(a)
		}
	}
	return &c
}

// sameJSON says whether two values decode to the same JSON.
func sameJSON(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	var u, w any
	_ = json.Unmarshal(x, &u)
	_ = json.Unmarshal(y, &w)
	return reflect.DeepEqual(u, w)
}
