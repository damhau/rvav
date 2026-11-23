package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/ad"
	"github.com/damhau/rvav/internal/naming"
	"github.com/damhau/rvav/internal/store"
)

// Naming policies (ADR-0020): offered to projects and environments, the
// most specific wins, else the blueprint's own template.

// namingSpec is where a request's VM names come from.
type namingSpec struct {
	Template naming.Template
	PolicyID string // empty for the blueprint's own template
	Name     string // the policy's name
	Counter  string // prefix or policy
}

// counterKey is the name counter a name with this prefix advances.
func (n namingSpec) counterKey(prefix string) string {
	if n.Counter == "policy" && n.PolicyID != "" {
		return "policy:" + n.PolicyID
	}
	return prefix
}

func (s *Service) namingFor(ctx context.Context, projectID, environment, blueprintNaming string) (namingSpec, error) {
	spec := namingSpec{Counter: "prefix"}
	raw := blueprintNaming
	pol, err := s.Store.NamingPolicyFor(ctx, projectID, environment)
	switch {
	case err == nil:
		raw, spec.PolicyID, spec.Name, spec.Counter = pol.Template, pol.ID, pol.Name, pol.Counter
	case !errors.Is(err, store.ErrNotFound):
		return spec, err
	}
	spec.Template, err = naming.Parse(raw)
	if pol.ID != "" {
		spec.Template = spec.Template.WithCounter(pol.Start, pol.Step)
	}
	return spec, err
}

type skippedName struct{ Name, In string }

// peekNames are the next names a spec gives with this prefix, those taken
// in vCenter, rvav, the directory (dir, when its VMs get computer objects)
// or DNS (under zone) passed over, and those given (to earlier
// components); nothing is reserved.
func (s *Service) peekNames(ctx context.Context, spec namingSpec, prefix, zone string, count int, given map[string]bool, dir *ad.Client) ([]string, []skippedName, *nameCheck, error) {
	vcenter, err := s.Store.InventoryVMNames(ctx, s.Store.Pool, prefix)
	if err != nil {
		return nil, nil, nil, err
	}
	ours, err := s.Store.DeploymentNames(ctx, s.Store.Pool, prefix)
	if err != nil {
		return nil, nil, nil, err
	}
	for n := range given {
		ours[n] = true
	}
	next, err := s.Store.PeekName(ctx, spec.counterKey(prefix), spec.Template.Start(), spec.Template.Step())
	if err != nil {
		return nil, nil, nil, err
	}
	check := &nameCheck{ctx: ctx, ipam: s.IPAM, vcenter: vcenter, rvav: ours, zone: zone, dir: dir}
	var names []string
	var skipped []skippedName
	for len(names) < count && check.err == nil {
		name := spec.Template.Name(prefix, next)
		next += spec.Template.Step()
		if in := check.takenIn(name); in != "" {
			skipped = append(skipped, skippedName{name, in})
			continue
		}
		names = append(names, name)
	}
	return names, skipped, check, nil
}

func presentNamingPolicy(p store.NamingPolicy, offers []store.Offer) api.NamingPolicy {
	return api.NamingPolicy{Id: p.ID, Name: p.Name, Template: p.Template, Counter: api.NamingPolicyCounter(p.Counter),
		Start: p.Start, Increment: p.Step, Offers: presentOffers(offers)}
}

func (s *Service) NamingPolicies(ctx context.Context, p *auth.Principal) ([]api.NamingPolicy, error) {
	if !p.Everywhere(auth.PoliciesRead) {
		return nil, ErrForbidden
	}
	list, err := s.Store.NamingPolicies(ctx)
	if err != nil {
		return nil, err
	}
	offers, err := s.Store.Offers(ctx, "naming")
	if err != nil {
		return nil, err
	}
	out := make([]api.NamingPolicy, len(list))
	for i, n := range list {
		out[i] = presentNamingPolicy(n, offers[n.ID])
	}
	return out, nil
}

// checkNamingTemplate says what is wrong with a naming template.
func checkNamingTemplate(raw, ptr string) []api.FieldError {
	tpl, err := naming.Parse(raw)
	if err != nil {
		return []api.FieldError{fieldErr(ptr, "invalid", "A template ends with {seq} or {seq:03}, for example {project}-{environment}-{role}{seq:03}.")}
	}
	// A sample name must be a host name.
	prefix, err := tpl.Prefix(map[string]string{"project": "web", "environment": "dev", "role": "app"})
	if err == nil && !naming.Valid(tpl.Name(prefix, 1)) {
		return []api.FieldError{fieldErr(ptr, "invalid", tpl.Name(prefix, 1)+" would not be a valid host name: lower case, digits and dashes.")}
	}
	return nil
}

// counterOf is a policy's counter start and increment, 1 and 1 when not given.
func counterOf(start, increment *int) (int, int) {
	s, i := 1, 1
	if start != nil {
		s = *start
	}
	if increment != nil {
		i = *increment
	}
	return s, i
}

func checkCounter(start, step int) []api.FieldError {
	var errs []api.FieldError
	if start < 0 || start > maxCounterStart {
		errs = append(errs, fieldErr("/start", "out_of_range", fmt.Sprintf("The counter starts between 0 and %d.", maxCounterStart)))
	}
	if step < 1 || step > maxCounterStep {
		errs = append(errs, fieldErr("/increment", "out_of_range", fmt.Sprintf("The counter moves by 1 to %d.", maxCounterStep)))
	}
	return errs
}

const (
	maxCounterStart = 999_999_999
	maxCounterStep  = 1000
)

func (s *Service) SaveNamingPolicy(ctx context.Context, p *auth.Principal, id string, w api.NamingPolicyWrite) (api.NamingPolicy, error) {
	if !p.Everywhere(auth.PoliciesManage) {
		return api.NamingPolicy{}, ErrForbidden
	}
	if id != "" {
		if _, err := s.Store.GetNamingPolicy(ctx, id); errors.Is(err, store.ErrNotFound) {
			return api.NamingPolicy{}, ErrNotFound
		} else if err != nil {
			return api.NamingPolicy{}, err
		}
	}
	var errs []api.FieldError
	if strings.TrimSpace(w.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the policy a name."))
	}
	errs = append(errs, checkNamingTemplate(w.Template, "/template")...)
	counter := "prefix"
	if w.Counter != nil {
		counter = string(*w.Counter)
		if !w.Counter.Valid() {
			errs = append(errs, fieldErr("/counter", "enum", "prefix or policy."))
		}
	}
	start, step := counterOf(w.Start, w.Increment)
	errs = append(errs, checkCounter(start, step)...)
	offers, offerErrs, err := s.checkOffers(ctx, w.Offers)
	if err != nil {
		return api.NamingPolicy{}, err
	}
	if errs = append(errs, offerErrs...); len(errs) > 0 {
		return api.NamingPolicy{}, &ValidationError{Errors: errs}
	}
	action := "naming.update"
	if id == "" {
		id, action = ids.New("nmp"), "naming.create"
	}
	pol := store.NamingPolicy{ID: id, Name: strings.TrimSpace(w.Name), Template: w.Template, Counter: counter, Start: start, Step: step}
	if err := s.Store.SaveNamingPolicy(ctx, pol, offers); err != nil {
		return api.NamingPolicy{}, err
	}
	s.audit(ctx, p, action, "naming_policy", id, nil, w)
	return presentNamingPolicy(pol, offers), nil
}

func (s *Service) DeleteNamingPolicy(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Everywhere(auth.PoliciesManage) {
		return ErrForbidden
	}
	name := s.Store.TargetName(ctx, "naming_policy", id)
	if err := s.Store.DeleteNamingPolicy(ctx, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.auditGone(ctx, p, "naming.delete", "naming_policy", id, name, nil, nil)
	return nil
}

// PreviewNaming gives the next names a template would give a project and
// environment, checked as a request's are; nothing is reserved.
func (s *Service) PreviewNaming(ctx context.Context, p *auth.Principal, q api.NamingPreviewQuery) (api.NamingPreview, error) {
	out := api.NamingPreview{Names: []string{}, Skipped: []api.NamingSkipped{}}
	if !p.Everywhere(auth.PoliciesManage) {
		return out, ErrForbidden
	}
	start, step := counterOf(q.Start, q.Increment)
	if errs := append(checkNamingTemplate(q.Template, "/template"), checkCounter(start, step)...); len(errs) > 0 {
		return out, &ValidationError{Errors: errs}
	}
	proj, err := s.visibleProject(ctx, p, q.ProjectId)
	if err != nil {
		return out, err
	}
	spec := namingSpec{Counter: "prefix", PolicyID: deref(q.PolicyId)}
	if q.Counter != nil {
		spec.Counter = string(*q.Counter)
	}
	spec.Template, _ = naming.Parse(q.Template)
	spec.Template = spec.Template.WithCounter(start, step)
	inputs := map[string]any{}
	if q.Inputs != nil {
		inputs = *q.Inputs
	}
	prefix, err := spec.Template.Prefix(naming.Values(proj.Code, q.Environment, inputs))
	if err != nil {
		msg := "Give the inputs the template uses (" + err.Error() + ")."
		out.Problem = &msg
		return out, nil
	}
	count := 3
	if q.Count != nil {
		count = *q.Count
	}
	names, skipped, check, err := s.peekNames(ctx, spec, prefix, "", count, nil, nil)
	if err != nil {
		return out, err
	}
	out.Names = append(out.Names, names...)
	for _, k := range skipped {
		out.Skipped = append(out.Skipped, api.NamingSkipped{Name: k.Name, TakenIn: api.NamingSkippedTakenIn(k.In)})
	}
	if check.err != nil {
		msg := check.err.Error()
		out.Problem = &msg
	}
	return out, nil
}
