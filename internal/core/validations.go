package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ohler55/ojg/jp"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/restcall"
	"github.com/damhau/rvav/internal/store"
)

// Validations (ADR-0043): checks of the whole form, REST or CEL, written
// in the form or named from the form library, run on submit once the
// inputs pass the form's own checks. A validation that can't run refuses
// the request: it never passes for want of an answer.

// checkMessage is what a validation says: a message on a field (a form's
// input, or a library entry's parameter), or on the form when field is "".
// An empty message is the validation's own.
type checkMessage struct{ field, message string }

func validationsOf(ui api.UiSchema) []api.FormValidation {
	if ui.Validations == nil {
		return nil
	}
	return *ui.Validations
}

// checkValidations checks a form's validations as written; props are its
// inputs, its property groups' among them.
func checkValidations(ui api.UiSchema, props map[string]any, prefix string) []api.FieldError {
	var errs []api.FieldError
	keys := map[string]bool{}
	for i, v := range validationsOf(ui) {
		at := fmt.Sprintf("%s/ui_schema/validations/%d", prefix, i)
		add := func(ptr, code, msg string) { errs = append(errs, fieldErr(at+ptr, code, msg)) }
		switch {
		case !libraryParam.MatchString(v.Key):
			add("/key", "pattern", "Letters, digits and underscores, starting with a letter.")
		case keys[v.Key]:
			add("/key", "duplicate", fmt.Sprintf("The form has two validations %s.", v.Key))
		}
		keys[v.Key] = true
		for j, f := range derefSlice(v.Fields) {
			if _, ok := props[f]; !ok {
				add(fmt.Sprintf("/fields/%d", j), "unknown_input", fmt.Sprintf("%q is not an input of the form's schema.", f))
			}
		}
		switch v.Kind {
		case api.Rest, api.Cel:
			if v.Name != nil || v.Version != nil {
				add("/name", "invalid", "Only a validation from the form library names an entry.")
			}
			errs = append(errs, checkCheckParams(v.Kind, derefMap(v.Params), at+"/params", "inputs")...)
		case api.Library:
			errs = append(errs, checkLibraryBindings(v.Name, v.Version, v.Params, at, props)...)
		default:
			add("/kind", "invalid", "A validation is rest, cel, or an entry of the form library (library).")
		}
	}
	return errs
}

// checkCheckParams checks a validation's parameters, whose templates and
// expressions read ns: a form's inputs, or a library entry's params.
func checkCheckParams(kind api.ValueSourceKind, params map[string]any, ptr, ns string) []api.FieldError {
	str := func(k string) string { s, _ := params[k].(string); return s }
	var errs []api.FieldError
	switch kind {
	case api.Rest:
		errs = append(errs, checkRESTParams(params, ptr, ns, "The URL of the service that checks the form")...)
		for _, k := range []string{"valid", "message", "errors"} {
			errs = append(errs, checkJSONPath(str(k), ptr+"/"+k)...)
		}
	case api.Cel:
		if strings.TrimSpace(str("check")) == "" {
			errs = append(errs, fieldErr(ptr+"/check", "required", "Give the check's expression: true when the form passes, or a message."))
		} else if err := checkCELOf(ns)(str("check")); err != nil {
			errs = append(errs, fieldErr(ptr+"/check", "invalid", "This expression is not valid CEL: "+err.Error()))
		}
	}
	return errs
}

// checkValidationRefs checks what a form's REST validations depend on:
// their host is allowed and their secret exists.
func (s *Service) checkValidationRefs(ctx context.Context, ui api.UiSchema, prefix string) ([]api.FieldError, error) {
	var errs []api.FieldError
	for i, v := range validationsOf(ui) {
		if v.Kind != api.Rest {
			continue
		}
		e, err := s.checkRESTRefs(ctx, derefMap(v.Params), fmt.Sprintf("%s/ui_schema/validations/%d/params", prefix, i))
		if err != nil {
			return nil, err
		}
		errs = append(errs, e...)
	}
	return errs, nil
}

// checkSpec is a validation to run: REST or CEL, and its parameters.
type checkSpec struct {
	kind   api.ValueSourceKind
	params map[string]any
}

func entryCheck(e store.LibraryEntry) checkSpec {
	params := map[string]any{}
	_ = json.Unmarshal(e.Params, &params)
	return checkSpec{kind: api.ValueSourceKind(e.Kind), params: params}
}

// formProps are the inputs of a version's form schema.
func formProps(schema json.RawMessage) map[string]any {
	var s struct {
		Properties map[string]any `json:"properties"`
	}
	_ = json.Unmarshal(schema, &s)
	if s.Properties == nil {
		return map[string]any{}
	}
	return s.Properties
}

// verdict is what a validation said, or why it couldn't run.
type verdict struct {
	messages []checkMessage
	failure  string
	host     string
}

func (v verdict) outcome() string {
	switch {
	case v.failure != "":
		return "failed"
	case len(v.messages) > 0:
		return "refused"
	}
	return "ok"
}

// runCheck runs a validation: a REST one's templates read refs, opening
// the project's secret; a CEL one's expression runs with eval over vars.
func (s *Service) runCheck(ctx context.Context, projectID string, c checkSpec, refs Refs, vars policy.Vars,
	eval func(string, policy.Vars) (any, error)) verdict {
	switch c.kind {
	case api.Rest:
		target, _ := c.params["url"].(string)
		v := verdict{host: hostOf(fill(target, nil, "", restcall.Escape))}
		secret := func(name string) (store.Secret, string, error) { return s.secretValue(ctx, projectID, name) }
		msgs, err := s.REST.Check(ctx, c.params, refs, secret)
		if err != nil {
			v.failure = err.Error()
			return v
		}
		v.messages = msgs
		return v
	case api.Cel:
		expr, _ := c.params["check"].(string)
		msgs, err := celCheck(expr, vars, eval)
		if err != nil {
			return verdict{failure: "its expression failed: " + err.Error()}
		}
		return verdict{messages: msgs}
	}
	return verdict{failure: fmt.Sprintf("%q is no kind of validation", c.kind)}
}

// celCheck reads a CEL validation's result: true, null, "" or {} passes;
// false refuses; text is the refusal's message; a map, a message per
// field; a list, messages.
func celCheck(expr string, vars policy.Vars, eval func(string, policy.Vars) (any, error)) ([]checkMessage, error) {
	v, err := eval(expr, vars)
	if err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case nil:
		return nil, nil
	case bool:
		if x {
			return nil, nil
		}
		return []checkMessage{{}}, nil
	case string:
		if x == "" {
			return nil, nil
		}
		return []checkMessage{{message: x}}, nil
	case map[string]any:
		var out []checkMessage
		for _, k := range sortedKeys(x) {
			if m := messageText(x[k]); m != "" {
				out = append(out, checkMessage{field: k, message: m})
			}
		}
		return out, nil
	case []any:
		var out []checkMessage
		for _, item := range x {
			if m := messageText(item); m != "" {
				out = append(out, checkMessage{message: m})
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("it gives %T: give true, false, a message, or a map of messages", v)
}

func messageText(v any) string {
	switch m := v.(type) {
	case nil, bool:
		return ""
	case string:
		return strings.TrimSpace(m)
	}
	return fmt.Sprint(v)
}

// readCheck reads a REST validation's answer: it passes when valid is
// true, or, without valid, when it has no message and no errors. A valid
// path the validation names must be in the answer; without one, it is
// $.valid. An empty answer can't be read.
func readCheck(data any, validPath, messagePath, errorsPath string) ([]checkMessage, error) {
	if data == nil {
		return nil, errors.New("the answer is empty")
	}
	named := validPath != ""
	validPath = cmp.Or(validPath, "$.valid")
	get := func(name, path string) (any, error) {
		x, err := jp.ParseString(path)
		if err != nil {
			return nil, fmt.Errorf("the %s path %q is not valid JSONPath: %v", name, path, err)
		}
		if got := x.Get(data); len(got) > 0 {
			return got[0], nil
		}
		return nil, nil
	}
	valid, err := get("valid", validPath)
	if err != nil {
		return nil, err
	}
	if valid == nil && named {
		return nil, fmt.Errorf("the answer has no %s", validPath)
	}
	if valid != nil {
		ok, isBool := valid.(bool)
		if !isBool {
			return nil, fmt.Errorf("the answer's %s is %v, not true or false", validPath, valid)
		}
		if ok {
			return nil, nil
		}
	}
	var out []checkMessage
	found, err := get("errors", errorsPath)
	if err != nil {
		return nil, err
	}
	switch e := found.(type) {
	case map[string]any:
		for _, k := range sortedKeys(e) {
			if m := messageText(e[k]); m != "" {
				out = append(out, checkMessage{field: k, message: m})
			}
		}
	case []any:
		for _, item := range e {
			if m, ok := item.(map[string]any); ok {
				if text := messageText(m["message"]); text != "" {
					out = append(out, checkMessage{field: str(m["field"]), message: text})
				}
			} else if text := messageText(item); text != "" {
				out = append(out, checkMessage{message: text})
			}
		}
	case string:
		if text := strings.TrimSpace(e); text != "" {
			out = append(out, checkMessage{message: text})
		}
	}
	msg, err := get("message", messagePath)
	if err != nil {
		return nil, err
	}
	if text := messageText(msg); text != "" {
		out = append(out, checkMessage{message: text})
	}
	if valid != nil && len(out) == 0 {
		out = []checkMessage{{}}
	}
	return out, nil
}

// refusedText is a refusal's message when the validation gave none.
func refusedText(message, title string) string {
	if strings.TrimSpace(message) != "" {
		return message
	}
	return title + " refused the request."
}

var inputAlone = regexp.MustCompile(`^\s*inputs(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[\s*"([^"]+)"\s*\])\s*$`)

// boundInput is the input a parameter's expression reads alone, if any.
func boundInput(bindings map[string]any, param string) string {
	expr, _ := bindings[param].(string)
	m := inputAlone.FindStringSubmatch(expr)
	if m == nil {
		return ""
	}
	return m[1] + m[2]
}

// evaluateValidations runs the form's validations once its inputs pass its
// own checks, each audited. Their messages, and why one couldn't run, are
// errors of the evaluation.
func (s *Service) evaluateValidations(ctx context.Context, p *auth.Principal, ev *Evaluation, ui api.UiSchema) {
	list := validationsOf(ui)
	if len(list) == 0 || len(ev.Errors) > 0 {
		return
	}
	props := formProps(ev.Version.FormSchema)
	vars := formVars(ev.Inputs, ev.Project, ev.Env.Key, p, ev.Version)
	for _, v := range list {
		ev.Errors = append(ev.Errors, s.runValidation(ctx, p, ev, v, props, vars)...)
	}
}

func (s *Service) runValidation(ctx context.Context, p *auth.Principal, ev *Evaluation, v api.FormValidation, props map[string]any,
	vars policy.Vars) []api.FieldError {
	title := cmp.Or(deref(v.Title), v.Key)
	message := deref(v.Message)
	detail := map[string]any{"validation": v.Key, "kind": v.Kind, "version_id": ev.Version.ID}
	var res verdict
	switch v.Kind {
	case api.Library:
		detail["form_library"], detail["entry_version"] = deref(v.Name), derefInt(v.Version)
		bindings := derefMap(v.Params)
		e, params, err := s.libraryBound(ctx, deref(v.Name), derefInt(v.Version), api.FormLibraryPurposeValidation, bindings, vars, props)
		switch {
		case errors.Is(err, errUnbound):
			detail["outcome"] = "not_run"
			s.audit(ctx, p, "form_validation.run", "catalog_item", ev.Item.ID, &ev.Project.ID, detail)
			return nil
		case err != nil:
			res.failure = err.Error()
		default:
			detail["entry_kind"] = e.Kind
			res = s.runCheck(ctx, ev.Project.ID, entryCheck(e), Refs{"params": params}, libraryVars(params, vars), policy.EvalLibrary)
			message = cmp.Or(message, e.Message)
			for i, m := range res.messages {
				if in := boundInput(bindings, m.field); in != "" {
					res.messages[i].field = in
				}
			}
		}
	default:
		res = s.runCheck(ctx, ev.Project.ID, checkSpec{kind: v.Kind, params: derefMap(v.Params)}, inputRefs(ev.Inputs), vars, policy.EvalSource)
	}
	detail["outcome"] = res.outcome()
	if res.host != "" {
		detail["host"] = res.host
	}
	s.audit(ctx, p, "form_validation.run", "catalog_item", ev.Item.ID, &ev.Project.ID, detail)
	at := "/validations/" + v.Key
	if res.failure != "" {
		return []api.FieldError{fieldErr(at, "validation_failed", fmt.Sprintf(
			"%s couldn't check the request (%s), so it can't be submitted: try again, and if it keeps failing, tell the catalog's administrators.",
			title, strings.TrimSuffix(res.failure, ".")))}
	}
	var errs []api.FieldError
	for _, m := range res.messages {
		text := cmp.Or(m.message, refusedText(message, title))
		if _, ok := props[m.field]; ok && m.field != "" {
			errs = append(errs, fieldErr("/inputs/"+m.field, "refused", text))
			continue
		}
		if m.field != "" {
			text = m.field + ": " + text
		}
		placed := false
		for _, f := range derefSlice(v.Fields) {
			if _, ok := props[f]; ok {
				errs = append(errs, fieldErr("/inputs/"+f, "refused", text))
				placed = true
			}
		}
		if !placed {
			errs = append(errs, fieldErr(at, "refused", text))
		}
	}
	return errs
}
