package core

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/restcall"
	"github.com/damhau/rvav/internal/secretinput"
	"github.com/damhau/rvav/internal/store"
)

// secretMask is what a reader sees of a secret input (ADR-0038).
const secretMask = "••••"

// readsRunData says whether p reads the run data and the shared variables
// (ADR-0057): they hold a blueprint's literals, which catalog managers read
// (ADR-0033), and auditors read them, as they read the subscriptions' runs.
func readsRunData(p *auth.Principal) bool {
	return p.Everywhere(auth.CatalogManage) || p.Everywhere(auth.ExtensibilityRead)
}

// StepData is what a step of a request was given and gave (ADR-0057), as this
// reader may see it: a secret input masked, its value removed from any text
// that repeats it, and a person's answer only for those who may read it.
func (s *Service) StepData(ctx context.Context, p *auth.Principal, req store.RequestRow, stepID string) (api.StepData, error) {
	if !readsRunData(p) {
		return api.StepData{}, ErrForbidden
	}
	st, err := s.Store.GetStep(ctx, req.ID, stepID)
	if errors.Is(err, store.ErrNotFound) {
		return api.StepData{}, ErrNotFound
	}
	if err != nil {
		return api.StepData{}, err
	}
	out := api.StepData{StepId: st.ID}
	var inputs, outputs map[string]any
	_ = json.Unmarshal(st.Inputs, &inputs)
	_ = json.Unmarshal(st.Outputs, &outputs)
	if outputs != nil && st.System != nil && *st.System == string(api.StepSystemPerson) {
		ok, err := s.readsAnswer(ctx, p, req.ID, st.Key)
		if err != nil {
			return out, err
		}
		if !ok {
			outputs, out.OutputsHidden = nil, true
		}
	}
	sealed, err := s.Store.RequestSecrets(ctx, req.ID)
	if err != nil {
		return out, err
	}
	values, err := secretinput.Values(s.Secrets, req.ID, sealed)
	if err != nil {
		return out, err
	}
	scrub := restcall.NewScrubber(values...)
	clean := func(m map[string]any) *map[string]any {
		if m == nil {
			return nil
		}
		v, _ := scrub.Value(secretinput.Mask(m, secretMask)).(map[string]any)
		return &v
	}
	out.Inputs, out.Outputs = clean(inputs), clean(outputs)
	return out, nil
}

// readsAnswer says whether p may read what a step's wait for a person was
// answered, as its question on the request's page says (ADR-0033).
func (s *Service) readsAnswer(ctx context.Context, p *auth.Principal, requestID, stepKey string) (bool, error) {
	if p.Everywhere(auth.ApprovalReadAll) {
		return true, nil
	}
	levels, err := s.Store.AllApprovalsForRequest(ctx, requestID)
	if err != nil {
		return false, err
	}
	for _, lv := range levels {
		if lv.StepKey == nil || *lv.StepKey != stepKey {
			continue
		}
		l, err := s.levelContext(ctx, lv)
		if err != nil {
			return false, err
		}
		if l.isApprover(p) {
			return true, nil
		}
	}
	return false, nil
}
