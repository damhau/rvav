package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/store"
)

// A composite step that waits for a person (ADR-0033): what it may say on
// save, who it leaves when requested, and what it may be answered.

const (
	maxAskFields = 20
	maxAskText   = 2000
	// maxAskAnswer bounds one answer: it travels through Temporal's history
	// to every later step.
	maxAskAnswer = 4096
)

var askFieldTypes = []string{"string", "number", "boolean", "choice"}

// checkAsk checks a step's wait on its own (the users it names are
// checkSourceRefs').
func checkAsk(a blueprint.Ask, ptr string) []api.FieldError {
	var errs []api.FieldError
	if a.Group == "" && len(a.Approvers) == 0 && !a.Requester && !a.Owner {
		errs = append(errs, fieldErr(ptr, "required", "Say who is asked: an OIDC group, approvers, the requester or the owner."))
	}
	if len(a.Message) > maxAskText {
		errs = append(errs, fieldErr(ptr+"/message", "too_long", fmt.Sprintf("At most %d characters.", maxAskText)))
	}
	if a.Timeout != "" {
		if d, err := time.ParseDuration(a.Timeout); err != nil || d < time.Second {
			errs = append(errs, fieldErr(ptr+"/timeout", "invalid", "A duration such as 48h."))
		}
	}
	if a.OnTimeout != "" {
		if !slices.Contains([]string{"fail", "continue", "escalate"}, a.OnTimeout) {
			errs = append(errs, fieldErr(ptr+"/on_timeout", "enum", "fail, continue or escalate."))
		}
		if a.Timeout == "" {
			errs = append(errs, fieldErr(ptr+"/on_timeout", "required", "What happens on time-out needs a timeout."))
		}
	}
	if a.OnTimeout == "escalate" && (a.EscalateTo == nil || a.EscalateTo.Group == "" && len(a.EscalateTo.Approvers) == 0) {
		errs = append(errs, fieldErr(ptr+"/escalate_to", "required", "Say whom to escalate to: an OIDC group or approvers."))
	}
	if len(a.Form) > maxAskFields {
		errs = append(errs, fieldErr(ptr+"/form", "too_many", fmt.Sprintf("At most %d questions.", maxAskFields)))
	}
	seen := map[string]bool{}
	for i, f := range a.Form {
		at := fmt.Sprintf("%s/form/%d", ptr, i)
		if !compositeKey.MatchString(f.Key) {
			errs = append(errs, fieldErr(at+"/key", "pattern", "A key of lower-case letters, digits and underscores."))
		} else if seen[f.Key] {
			errs = append(errs, fieldErr(at+"/key", "duplicate", "Another question has the key "+f.Key+"."))
		}
		seen[f.Key] = true
		if strings.TrimSpace(f.Label) == "" {
			errs = append(errs, fieldErr(at+"/label", "required", "Say what it asks."))
		}
		// The answer goes through Temporal's history to every later step.
		if f.Secret || f.Type == "secret" || f.Type == "password" {
			errs = append(errs, fieldErr(at, "secret", "A step's form can't ask for a secret: its answer travels through Temporal's history to every later step. Keep secrets in the request's form or a stored secret."))
			continue
		}
		if !slices.Contains(askFieldTypes, f.Type) {
			errs = append(errs, fieldErr(at+"/type", "enum", "string, number, boolean or choice."))
			continue
		}
		if f.Type == "choice" && len(f.Options) == 0 {
			errs = append(errs, fieldErr(at+"/options", "required", "List what it may be."))
		}
		if f.Type != "choice" && len(f.Options) > 0 {
			errs = append(errs, fieldErr(at+"/options", "invalid", "Only a choice has options."))
		}
		if f.Default != nil {
			if why := askValueProblem(f, f.Default); why != "" {
				errs = append(errs, fieldErr(at+"/default", "invalid", why))
			}
		}
	}
	return errs
}

// askValueProblem says what is wrong with a value for a question, if anything.
func askValueProblem(f blueprint.AskField, v any) string {
	switch f.Type {
	case "string":
		if s, ok := v.(string); !ok {
			return "Give text."
		} else if len(s) > maxAskAnswer {
			return fmt.Sprintf("At most %d characters.", maxAskAnswer)
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return "Give a number."
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return "Give true or false."
		}
	case "choice":
		if s, ok := v.(string); !ok || !slices.Contains(f.Options, s) {
			return "Choose one of: " + strings.Join(f.Options, ", ") + "."
		}
	}
	return ""
}

// checkAskAnswer checks an answer against the step's form, and gives it
// without what was left blank.
func checkAskAnswer(form []blueprint.AskField, answer map[string]any) (map[string]any, []api.FieldError) {
	var errs []api.FieldError
	for _, k := range sortedKeys(answer) {
		if !slices.ContainsFunc(form, func(f blueprint.AskField) bool { return f.Key == k }) {
			errs = append(errs, fieldErr("/answer/"+k, "unknown", "This form doesn't ask this."))
		}
	}
	out := map[string]any{}
	for _, f := range form {
		v := answer[f.Key]
		if v == nil || v == "" {
			if f.Required {
				errs = append(errs, fieldErr("/answer/"+f.Key, "required", "Answer this."))
			}
			continue
		}
		if why := askValueProblem(f, v); why != "" {
			errs = append(errs, fieldErr("/answer/"+f.Key, "invalid", why))
			continue
		}
		out[f.Key] = v
	}
	return out, errs
}

// asks are a composite's waits for a person, by pointer.
func asks(b blueprint.Blueprint, ptr string) map[string]*blueprint.Ask {
	out := map[string]*blueprint.Ask{}
	eachStep(b, ptr, func(s blueprint.CompositeStep, at string) {
		if s.Ask != nil {
			out[at+"/ask"] = s.Ask
		}
	})
	return out
}

// checkAskUsers checks that the users a composite's waits name exist.
func (s *Service) checkAskUsers(ctx context.Context, b blueprint.Blueprint, ptr string) ([]api.FieldError, error) {
	var errs []api.FieldError
	list := asks(b, ptr)
	for _, at := range sortedKeys(list) {
		a := list[at]
		named := slices.Clone(a.Approvers)
		if a.EscalateTo != nil {
			named = append(named, a.EscalateTo.Approvers...)
		}
		if len(named) == 0 {
			continue
		}
		found, err := s.Store.FindUsers(ctx, named)
		if err != nil {
			return nil, err
		}
		for _, id := range named {
			if _, ok := found[id]; !ok {
				errs = append(errs, fieldErr(at, "unknown", "There is no user "+id+"."))
			}
		}
	}
	return errs, nil
}

// asksNobodyElse says whether a step, requested by this person, leaves nobody
// to answer: it names only them, who don't answer their own request unless
// it asks the requester or owner.
func asksNobodyElse(a *blueprint.Ask, requesterID string) bool {
	if a.Group != "" || a.Requester || a.Owner {
		return false
	}
	return !slices.ContainsFunc(a.Approvers, func(id string) bool { return id != requesterID })
}

// stepForm is what a step's row asks; it is empty when it asks to approve or
// reject, and for an approval of the request.
func stepForm(lv store.Approval) []blueprint.AskField {
	var form []blueprint.AskField
	if lv.StepKey != nil && len(lv.Form) > 0 {
		_ = json.Unmarshal(lv.Form, &form)
	}
	return form
}

// question is what a step's row asks and got, for those who may read it.
func question(l loadedLevel, p *auth.Principal) *api.ApprovalQuestion {
	if l.lv.StepKey == nil || !l.isApprover(p) && !p.SeesEverything() {
		return nil
	}
	q := &api.ApprovalQuestion{Form: []api.ApprovalQuestionField{}}
	if l.lv.Message != "" {
		q.Message = &l.lv.Message
	}
	for _, f := range stepForm(l.lv) {
		qf := api.ApprovalQuestionField{Key: f.Key, Label: f.Label, Type: api.ApprovalQuestionFieldType(f.Type), Default: f.Default}
		if f.Required {
			qf.Required = &f.Required
		}
		if len(f.Options) > 0 {
			qf.Options = &f.Options
		}
		q.Form = append(q.Form, qf)
	}
	if len(l.lv.Answer) > 0 {
		var answer map[string]any
		if json.Unmarshal(l.lv.Answer, &answer) == nil {
			q.Answer = &answer
		}
	}
	return q
}
