package core

import (
	"context"
	"errors"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
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
	return spec, err
}

type skippedName struct{ Name, In string }

// peekNames are the next names a spec gives with this prefix, those taken
// in vCenter, rvav or DNS (under zone) passed over; nothing is reserved.
func (s *Service) peekNames(ctx context.Context, spec namingSpec, prefix, zone string, count int) ([]string, []skippedName, *nameCheck, error) {
	vcenter, err := s.Store.InventoryVMNames(ctx, prefix)
	if err != nil {
		return nil, nil, nil, err
	}
	ours, err := s.Store.DeploymentNames(ctx, prefix)
	if err != nil {
		return nil, nil, nil, err
	}
	next, err := s.Store.PeekName(ctx, spec.counterKey(prefix))
	if err != nil {
		return nil, nil, nil, err
	}
	check := &nameCheck{ctx: ctx, ipam: s.IPAM, vcenter: vcenter, rvav: ours, zone: zone}
	var names []string
	var skipped []skippedName
	for len(names) < count && check.err == nil {
		name := spec.Template.Name(prefix, next)
		next++
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
		Offers: presentOffers(offers)}
}

func (s *Service) NamingPolicies(ctx context.Context, p *auth.Principal) ([]api.NamingPolicy, error) {
	if !p.SeesEverything() {
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

func (s *Service) SaveNamingPolicy(ctx context.Context, p *auth.Principal, id string, w api.NamingPolicyWrite) (api.NamingPolicy, error) {
	if !p.IsAdmin() {
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
	pol := store.NamingPolicy{ID: id, Name: strings.TrimSpace(w.Name), Template: w.Template, Counter: counter}
	if err := s.Store.SaveNamingPolicy(ctx, pol, offers); err != nil {
		return api.NamingPolicy{}, err
	}
	s.audit(ctx, p, action, "naming_policy", id, nil, w)
	return presentNamingPolicy(pol, offers), nil
}

func (s *Service) DeleteNamingPolicy(ctx context.Context, p *auth.Principal, id string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	if err := s.Store.DeleteNamingPolicy(ctx, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "naming.delete", "naming_policy", id, nil, nil)
	return nil
}

// PreviewNaming gives the next names a template would give a project and
// environment, checked as a request's are; nothing is reserved.
func (s *Service) PreviewNaming(ctx context.Context, p *auth.Principal, q api.NamingPreviewQuery) (api.NamingPreview, error) {
	out := api.NamingPreview{Names: []string{}, Skipped: []api.NamingSkipped{}}
	if !p.IsAdmin() {
		return out, ErrForbidden
	}
	if errs := checkNamingTemplate(q.Template, "/template"); len(errs) > 0 {
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
	names, skipped, check, err := s.peekNames(ctx, spec, prefix, "", count)
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
