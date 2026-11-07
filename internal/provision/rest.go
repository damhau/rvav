package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/ohler55/ojg/jp"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/observe"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/restcall"
	"github.com/damhau/rvav/internal/secretinput"
	"github.com/damhau/rvav/internal/store"
)

// A composite's REST calls (ADR-0033).

// RESTTimeout bounds one call.
const RESTTimeout = 30 * time.Second

// maxKept is the most of an answer a step keeps: its outputs travel through
// Temporal's history, to every later step.
const maxKept = 256 << 10

// RESTInput is a REST call to make, for a step or its undo. It holds names
// and expressions only: what they stand for is resolved in RunREST, so no
// secret enters Temporal's history.
type RESTInput struct {
	RequestID string             `json:"request_id"`
	ProjectID string             `json:"project_id"`
	StepKey   string             `json:"step_key"`
	Call      blueprint.RESTCall `json:"call"`
	Steps     map[string]any     `json:"steps,omitempty"`
}

// restCtx runs a call of this method. A method that can be repeated is
// retried as other calls are; a POST or a PATCH isn't, even when the worker
// dies during it: what it did is unknown, so the step fails and a person
// decides.
func restCtx(ctx workflow.Context, method string) workflow.Context {
	o := activityOptions
	o.StartToCloseTimeout = 2 * RESTTimeout
	if !repeatable(method) {
		retry := *activityOptions.RetryPolicy
		retry.MaximumAttempts = 1
		o.RetryPolicy = &retry
	}
	return workflow.WithActivityOptions(ctx, o)
}

func repeatable(method string) bool {
	return method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
}

// RunREST makes a step's REST call and gives its outputs: the status and
// what is kept of the JSON answer. No answer's headers, nor anything of the
// secrets it used, in those, in the step's texts or in its errors. It leaves
// the step running: FinishStep closes it with its outputs.
func (a *Activities) RunREST(ctx context.Context, in RESTInput) (map[string]any, error) {
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "running"})
	start := time.Now()
	out, err := a.callREST(ctx, in)
	observe.Step(in.StepKey, err != nil, time.Since(start))
	if err != nil {
		msg := errMessage(err)
		a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "failed", Error: &msg})
		return nil, err
	}
	detail := fmt.Sprintf("%s answered %v", in.Call.Method, out["status"])
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Detail: &detail})
	return out, nil
}

func (a *Activities) callREST(ctx context.Context, in RESTInput) (map[string]any, error) {
	if a.REST == nil {
		return nil, permanent("This rvav makes no REST calls.")
	}
	call := in.Call
	vars, err := a.stepVars(ctx, in.RequestID, in.Steps)
	if err != nil {
		return nil, err
	}
	sensitive, err := a.secretInputValues(ctx, in.RequestID)
	if err != nil {
		return nil, permanent("%v", err)
	}
	target, host, err := restTarget(ctx, a.REST, call.URL, vars)
	if err != nil {
		return nil, err
	}
	req := restcall.Request{Method: call.Method, URL: target}
	if call.Secret != "" {
		sec, value, err := a.openSecret(ctx, call.Secret)
		if err != nil {
			return nil, err
		}
		req.Credential = &restcall.Credential{Kind: sec.Kind, Username: sec.Username, Header: sec.Header, Value: value}
		sensitive = append(sensitive, req.Credential.Sensitive()...)
	}
	scrub := restcall.NewScrubber(sensitive...)
	if call.Body != "" {
		if req.Body, err = a.restBody(ctx, in.RequestID, call.Body, vars); err != nil {
			return nil, permanent("%s", scrub.Text(err.Error()))
		}
	}
	answer, err := a.REST.Do(ctx, req)
	switch {
	case errors.Is(err, restcall.ErrTooLarge):
		return nil, permanent("%s %s: %v", call.Method, host, err)
	case errors.Is(err, restcall.ErrNotAllowed):
		return nil, permanent("rvav may not call %s (Administration › Integrations › Allowed hosts).", host)
	case err != nil:
		return nil, restFailure(call.Method, true, fmt.Sprintf("%s %s: %s", call.Method, host, scrub.Text(err.Error())))
	}
	if !accepted(call.Accept, answer.Status) {
		again := answer.Status >= 500 || answer.Status == http.StatusTooManyRequests || answer.Status == http.StatusRequestTimeout
		return nil, restFailure(call.Method, again, fmt.Sprintf("%s %s answered %s.", call.Method, host, restcall.StatusText(answer.Status)))
	}
	kept, err := keepOfAnswer(answer.Body, call.Keep)
	if err != nil {
		return nil, permanent("%v", err)
	}
	kept = scrub.Value(kept)
	if b, err := json.Marshal(kept); err != nil {
		return nil, permanent("What is kept of the answer can't be read: %v", err)
	} else if len(b) > maxKept {
		return nil, permanent("%s %s answered %d, but what is kept of it is over %d KiB: pick less with keep.", call.Method, host, answer.Status, maxKept>>10)
	}
	return map[string]any{"status": answer.Status, "json": kept}, nil
}

// restFailure is a call that failed: tried again if it might do better and
// its method can be repeated.
func restFailure(method string, again bool, msg string) error {
	if again && repeatable(method) {
		return temporal.NewApplicationError(msg, "RESTCall")
	}
	return permanent("%s", msg)
}

// restTarget fills a call's URL, and says its host. Errors name the
// expression with its literals masked, and only the URL's host: a requester
// reads them.
func restTarget(ctx context.Context, caller *restcall.Caller, tpl string, vars policy.Vars) (target, host string, err error) {
	parts, err := restcall.ParseURL(tpl)
	if err != nil {
		return "", "", permanent("The call's URL isn't valid: %v. Ask a catalog admin to check this item.", err)
	}
	if !restcall.FixedOrigin(tpl) {
		return "", "", permanent("The call's URL lets expressions choose its host. Ask a catalog admin to check this item.")
	}
	target, err = restcall.Fill(parts, func(expr string) (any, error) { return restEval(expr, vars) })
	if err != nil {
		var fe *restcall.FillError
		if errors.As(err, &fe) {
			return "", "", permanent("The call wasn't made: its URL's ${%s}: %v", policy.MaskLiterals(fe.Expr, false), fe.Err)
		}
		return "", "", permanent("The call wasn't made: %v", err)
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return "", "", permanent("The call's URL isn't valid. Ask a catalog admin to check this item.")
	}
	if !caller.Allowed(ctx, u) {
		return "", "", permanent("rvav may not call %s (Administration › Integrations › Allowed hosts).", u.Host)
	}
	return target, u.Host, nil
}

// restEval evaluates an expression of a call, whose errors a requester reads:
// none quotes its text, as a compile error would, its literals and all.
func restEval(expr string, vars policy.Vars) (any, error) {
	if policy.CheckComposite(expr) != nil {
		return nil, errors.New("it isn't valid CEL. Ask a catalog admin to check this item.")
	}
	return policy.CompositeEval(expr, vars)
}

// restBody evaluates a call's body and opens the secret inputs in it.
func (a *Activities) restBody(ctx context.Context, requestID, expr string, vars policy.Vars) ([]byte, error) {
	v, err := restEval(expr, vars)
	if err != nil {
		return nil, fmt.Errorf("The call wasn't made: its body: %v", err)
	}
	opened, err := a.openSecretInputs(ctx, requestID, map[string]any{"body": v})
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(opened["body"])
	if err != nil {
		return nil, fmt.Errorf("The call wasn't made: its body isn't JSON: %v", err)
	}
	return b, nil
}

// secretInputValues are the values of a request's secret inputs: an API may
// echo them, so they don't stay in an answer either.
func (a *Activities) secretInputValues(ctx context.Context, requestID string) ([]string, error) {
	sealed, err := a.Store.RequestSecrets(ctx, requestID)
	if err != nil || len(sealed) == 0 {
		return nil, err
	}
	vars := map[string]any{}
	for _, s := range sealed {
		vars[s.Field] = secretinput.Marker(s.Field)
	}
	if err := secretinput.Open(a.Secrets, requestID, sealed, vars); err != nil {
		return nil, err
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case nil:
		case string:
			out = append(out, x)
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		default:
			if b, err := json.Marshal(x); err == nil {
				out = append(out, string(b))
			}
		}
	}
	walk(vars)
	return out, nil
}

// accepted says whether a status is a success: one of those listed, or by
// default any 2xx.
func accepted(accept []int, status int) bool {
	if len(accept) > 0 {
		return slices.Contains(accept, status)
	}
	return status >= 200 && status < 300
}

// keepOfAnswer is what a step keeps of a JSON answer: all of it, or what the
// JSONPath picks. A path that can match several things gives a list, however
// many it matches; one that names one thing gives it, or null. An answer
// that isn't JSON gives null: its status is what it said.
func keepOfAnswer(raw []byte, path string) (any, error) {
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, nil
	}
	if path == "" {
		return data, nil
	}
	x, err := jp.ParseString(path)
	if err != nil {
		return nil, fmt.Errorf("The call's keep isn't valid JSONPath: %v. Ask a catalog admin to check this item.", err)
	}
	got := x.Get(data)
	if definite(x) {
		if len(got) == 0 {
			return nil, nil
		}
		return got[0], nil
	}
	if got == nil {
		got = []any{}
	}
	return got, nil
}

// definite says whether a path names one thing, rather than picks some.
func definite(x jp.Expr) bool {
	for _, f := range x {
		switch f.(type) {
		case jp.Root, jp.At, jp.Bracket, jp.Child, jp.Nth:
		default:
			return false
		}
	}
	return true
}
