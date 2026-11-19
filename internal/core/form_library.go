package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/restcall"
	"github.com/damhau/rvav/internal/store"
)

// The form library (ADR-0043): value sources and validations, REST or CEL,
// a new version at each save, which forms name by key and version. An
// entry takes parameters, which each form gives as CEL over its inputs; a
// new version gives the items using an earlier one a draft naming it.

var libraryParam = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

// ListFormLibrary lists the form library's entries at their latest version.
func (s *Service) ListFormLibrary(ctx context.Context, p *auth.Principal) (api.FormLibraryList, error) {
	out := api.FormLibraryList{Items: []api.FormLibraryEntry{}}
	if !p.Everywhere(auth.CatalogManage) {
		return out, ErrForbidden
	}
	list, err := s.Store.LibraryEntries(ctx)
	if err != nil {
		return out, err
	}
	for _, e := range list {
		pe, err := s.presentEntry(ctx, e)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, pe)
	}
	return out, nil
}

// FormLibraryEntry gives a library entry at a version, its latest when
// version is 0.
func (s *Service) FormLibraryEntry(ctx context.Context, p *auth.Principal, key string, version int) (api.FormLibraryEntry, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.FormLibraryEntry{}, ErrForbidden
	}
	e, err := s.Store.GetLibraryEntry(ctx, key, version)
	if errors.Is(err, store.ErrNotFound) {
		return api.FormLibraryEntry{}, ErrNotFound
	} else if err != nil {
		return api.FormLibraryEntry{}, err
	}
	return s.presentEntry(ctx, e)
}

// PutFormLibraryEntry adds a library entry (create, its key in w) or saves
// one as its next version, giving the items that use an earlier version a
// draft naming the new one.
func (s *Service) PutFormLibraryEntry(ctx context.Context, p *auth.Principal, key string, w api.FormLibraryEntryWrite, create bool) (api.FormLibraryEntry, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.FormLibraryEntry{}, ErrForbidden
	}
	if create {
		key = deref(w.Key)
	}
	var errs []api.FieldError
	if create && !groupKey.MatchString(key) {
		errs = append(errs, fieldErr("/key", "pattern", "Letters, digits and underscores, starting with a letter: forms name it by its key."))
	}
	if !create && w.Key != nil && *w.Key != key {
		errs = append(errs, fieldErr("/key", "invalid", "An entry's key doesn't change."))
	}
	if !create {
		latest, err := s.Store.GetLibraryEntry(ctx, key, 0)
		if errors.Is(err, store.ErrNotFound) {
			return api.FormLibraryEntry{}, ErrNotFound
		} else if err != nil {
			return api.FormLibraryEntry{}, err
		}
		if latest.Use != string(w.Use) {
			errs = append(errs, fieldErr("/use", "invalid", fmt.Sprintf("This entry is a %s: forms use it as one. Add another entry instead.", latest.Use)))
		}
	}
	errs = append(errs, checkEntry(w, "")...)
	refs, err := s.checkEntryRefs(ctx, w.Kind, w.Params, "")
	if err != nil {
		return api.FormLibraryEntry{}, err
	}
	if errs = append(errs, refs...); len(errs) > 0 {
		return api.FormLibraryEntry{}, &ValidationError{Errors: errs}
	}
	e := storeEntry(key, w)
	version, err := s.Store.AddLibraryEntryVersion(ctx, e, create, p.User.ID)
	switch {
	case errors.Is(err, store.ErrInUse) && create:
		return api.FormLibraryEntry{}, &ConflictError{Msg: fmt.Sprintf("The form library has an entry %q: save it as a new version instead.", key)}
	case errors.Is(err, store.ErrInUse):
		return api.FormLibraryEntry{}, &ConflictError{Msg: "This entry was saved elsewhere at the same time: open it again to see that version."}
	case errors.Is(err, store.ErrNotFound):
		return api.FormLibraryEntry{}, ErrNotFound
	case err != nil:
		return api.FormLibraryEntry{}, err
	}
	action := "form_library.update"
	if create {
		action = "form_library.create"
	}
	s.auditLibrary(ctx, p, action, key, map[string]any{"version": version, "use": w.Use, "kind": w.Kind, "host": entryHost(w.Kind, w.Params)})
	var drafts []api.FormLibraryDraft
	if !create {
		if drafts, err = s.libraryDrafts(ctx, p, key, version); err != nil {
			return api.FormLibraryEntry{}, err
		}
	}
	out, err := s.FormLibraryEntry(ctx, p, key, version)
	if err == nil && !create {
		if drafts == nil {
			drafts = []api.FormLibraryDraft{}
		}
		out.Drafts = &drafts
	}
	return out, err
}

// DeleteFormLibraryEntry deletes a library entry, every version of it,
// when no item's version but a draft names it.
func (s *Service) DeleteFormLibraryEntry(ctx context.Context, p *auth.Principal, key string) error {
	if !p.Everywhere(auth.CatalogManage) {
		return ErrForbidden
	}
	users, err := s.Store.DeleteLibraryEntry(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if len(users) > 0 {
		return &ConflictError{Msg: "Items use this entry: " + strings.Join(users, ", ") + "."}
	}
	s.auditLibrary(ctx, p, "form_library.delete", key, nil)
	return nil
}

// TestFormLibraryEntry runs an entry, saved or not, with its parameters'
// values, in a project when given (whose secret it opens).
func (s *Service) TestFormLibraryEntry(ctx context.Context, p *auth.Principal, t api.FormLibraryTest) (api.FormLibraryTestResult, error) {
	var out api.FormLibraryTestResult
	if !p.Everywhere(auth.CatalogManage) {
		return out, ErrForbidden
	}
	errs := checkEntry(t.Entry, "/entry")
	refs, err := s.checkEntryRefs(ctx, t.Entry.Kind, t.Entry.Params, "/entry")
	if err != nil {
		return out, err
	}
	if errs = append(errs, refs...); len(errs) > 0 {
		return out, &ValidationError{Errors: errs}
	}
	var proj store.Project
	if id := deref(t.ProjectId); id != "" {
		if proj, err = s.Store.GetProject(ctx, id); errors.Is(err, store.ErrNotFound) {
			return out, &ValidationError{Errors: []api.FieldError{fieldErr("/project_id", "unknown", "There is no such project.")}}
		} else if err != nil {
			return out, err
		}
	}
	vars := policy.Vars{"params": t.Params, "environment": deref(t.Environment), "project": projectVars(proj),
		"user": formVars(nil, proj, "", p, store.ItemVersion{})["user"], "search": deref(t.Search)}
	e := storeEntry("test", t.Entry)
	audit := map[string]any{"key": deref(t.Entry.Key), "use": t.Entry.Use, "kind": t.Entry.Kind, "host": entryHost(t.Entry.Kind, t.Entry.Params)}
	defer func() { s.auditLibrary(ctx, p, "form_library.test", cmp.Or(deref(t.Entry.Key), "unsaved"), audit) }()
	if e.Use == string(api.Validation) {
		v := s.runCheck(ctx, proj.ID, entryCheck(e), Refs{"params": t.Params}, vars, policy.EvalLibrary)
		audit["outcome"] = v.outcome()
		if v.failure != "" {
			return out, &BadRequestError{Msg: v.failure}
		}
		valid := len(v.messages) == 0
		msgs := make([]struct {
			Field   *string `json:"field,omitempty"`
			Message string  `json:"message"`
		}, 0, len(v.messages))
		for _, m := range v.messages {
			msg := m.message
			if msg == "" {
				msg = refusedText(e.Message, e.Name)
			}
			item := struct {
				Field   *string `json:"field,omitempty"`
				Message string  `json:"message"`
			}{Message: msg}
			if m.field != "" {
				item.Field = &m.field
			}
			msgs = append(msgs, item)
		}
		out.Valid, out.Messages = &valid, &msgs
		return out, nil
	}
	src := api.ValueSource{Kind: t.Entry.Kind, Params: &t.Entry.Params, Search: t.Entry.Search}
	secret := func(name string) (store.Secret, string, error) { return s.secretValue(ctx, proj.ID, name) }
	switch src.Kind {
	case api.Rest:
		opts, _, err := s.REST.Options(ctx, t.Entry.Params, Refs{"params": t.Params}, deref(t.Search), secret)
		if err != nil {
			audit["outcome"] = "failed"
			return out, err
		}
		out.Options = &opts
		if value, _, err := s.REST.Value(ctx, t.Entry.Params, Refs{"params": t.Params}, secret); err == nil {
			out.Value = value
		}
	case api.Cel:
		opts, value, err := celValues(t.Entry.Params, vars, policy.EvalLibrary)
		if err != nil {
			audit["outcome"] = "failed"
			return out, err
		}
		out.Options, out.Value = opts, value
	}
	audit["outcome"] = "ok"
	return out, nil
}

func (s *Service) auditLibrary(ctx context.Context, p *auth.Principal, action, key string, detail map[string]any) {
	if err := s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: &p.User.ID, Action: action, TargetType: "form_library",
		TargetID: key, Detail: detail}); err != nil {
		s.Log.Error("audit", "action", action, "err", err)
	}
}

// entryHost is the host a REST entry calls, for the audit.
func entryHost(kind api.ValueSourceKind, params map[string]any) string {
	if kind != api.Rest {
		return ""
	}
	target, _ := params["url"].(string)
	return hostOf(fill(target, nil, "", restcall.Escape))
}

func storeEntry(key string, w api.FormLibraryEntryWrite) store.LibraryEntry {
	params, _ := json.Marshal(orEmpty(w.Params))
	parameters := w.Parameters
	if parameters == nil {
		parameters = []api.FormLibraryParameter{}
	}
	prm, _ := json.Marshal(parameters)
	return store.LibraryEntry{Key: key, Name: strings.TrimSpace(w.Name), Description: deref(w.Description), Use: string(w.Use),
		Kind: string(w.Kind), Params: params, Search: w.Search != nil && *w.Search, Parameters: prm, Message: deref(w.Message)}
}

func (s *Service) presentEntry(ctx context.Context, e store.LibraryEntry) (api.FormLibraryEntry, error) {
	out := api.FormLibraryEntry{Key: e.Key, Name: e.Name, Description: e.Description, Use: api.FormLibraryPurpose(e.Use),
		Kind: api.ValueSourceKind(e.Kind), Params: map[string]any{}, Search: e.Search, Parameters: []api.FormLibraryParameter{},
		Version: e.Version, UpdatedAt: e.CreatedAt, UsedBy: []api.FormLibraryItemUse{}}
	if e.Message != "" {
		out.Message = &e.Message
	}
	if err := json.Unmarshal(e.Params, &out.Params); err != nil {
		return out, err
	}
	if err := json.Unmarshal(e.Parameters, &out.Parameters); err != nil {
		return out, err
	}
	var err error
	if out.Versions, err = s.Store.LibraryEntryVersions(ctx, e.Key); err != nil {
		return out, err
	}
	uses, err := s.Store.LibraryUses(ctx, e.Key)
	if err != nil {
		return out, err
	}
	for _, u := range uses {
		out.UsedBy = append(out.UsedBy, api.FormLibraryItemUse{ItemId: u.ItemID, Slug: u.Slug, Name: u.Name, VersionId: u.VersionID,
			Number: u.Number, State: u.State, EntryVersion: u.EntryVersion})
	}
	return out, nil
}

// checkEntry checks a library entry as written, with pointers under prefix.
func checkEntry(w api.FormLibraryEntryWrite, prefix string) []api.FieldError {
	var errs []api.FieldError
	add := func(ptr, code, msg string) { errs = append(errs, fieldErr(prefix+ptr, code, msg)) }
	if strings.TrimSpace(w.Name) == "" {
		add("/name", "required", "Give the entry a name.")
	}
	if !w.Use.Valid() {
		add("/use", "invalid", "An entry is a source or a validation.")
	}
	if w.Kind != api.Rest && w.Kind != api.Cel {
		add("/kind", "invalid", "An entry calls a REST service (rest) or works it out in CEL (cel).")
		return errs
	}
	names := map[string]bool{}
	for i, prm := range w.Parameters {
		at := fmt.Sprintf("/parameters/%d", i)
		switch {
		case !libraryParam.MatchString(prm.Name):
			add(at+"/name", "pattern", "Letters, digits and underscores, starting with a letter: the entry reads it as params.<name>.")
		case names[prm.Name]:
			add(at+"/name", "duplicate", fmt.Sprintf("The entry has %s twice.", prm.Name))
		}
		names[prm.Name] = true
		if strings.TrimSpace(prm.Label) == "" {
			add(at+"/label", "required", "Give the parameter a label.")
		}
	}
	params := orEmpty(w.Params)
	if w.Use == api.Validation {
		if w.Search != nil && *w.Search {
			add("/search", "invalid", "A validation searches nothing: leave search out.")
		}
		return append(errs, checkCheckParams(w.Kind, params, prefix+"/params", "params")...)
	}
	if w.Message != nil && *w.Message != "" {
		add("/message", "invalid", "Only a validation has a message.")
	}
	src := api.ValueSource{Kind: w.Kind, Params: &params, Search: w.Search}
	return append(errs, checkSourceParams(src, prefix, "params")...)
}

// checkEntryRefs checks what a REST entry depends on: its host is allowed
// and its secret exists.
func (s *Service) checkEntryRefs(ctx context.Context, kind api.ValueSourceKind, params map[string]any, prefix string) ([]api.FieldError, error) {
	if kind != api.Rest {
		return nil, nil
	}
	return s.checkRESTRefs(ctx, params, prefix+"/params")
}

// checkRESTRefs checks a REST call's host is allowed and its secret exists.
func (s *Service) checkRESTRefs(ctx context.Context, params map[string]any, ptr string) ([]api.FieldError, error) {
	var errs []api.FieldError
	target, _ := params["url"].(string)
	if u, err := url.Parse(fill(target, nil, "", restcall.Escape)); err == nil && u.Host != "" && !s.REST.Allowed(ctx, u) {
		errs = append(errs, fieldErr(ptr+"/url", "not_allowed", fmt.Sprintf("rvav may not call %s: it is not one of the hosts REST value sources may call (Administration › Integrations › Allowed hosts).", u.Host)))
	}
	if secret, _ := params["secret"].(string); secret != "" {
		if _, err := s.anySecret(ctx, secret); errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr(ptr+"/secret", "unknown", fmt.Sprintf("There is no secret %q.", secret)))
		} else if err != nil {
			return nil, err
		}
	}
	return errs, nil
}

// libraryParameters are an entry's parameters.
func libraryParameters(e store.LibraryEntry) []api.FormLibraryParameter {
	var out []api.FormLibraryParameter
	_ = json.Unmarshal(e.Parameters, &out)
	return out
}

// errUnbound is a required parameter of a library entry its form gives no
// value: the entry isn't run.
var errUnbound = errors.New("a required parameter has no value")

// libraryBound gives the entry a form's reference names and the values
// its bindings give the entry's parameters, CEL over vars. A binding that
// fails, as one reading an input not given yet does, gives no value.
func (s *Service) libraryBound(ctx context.Context, key string, version int, use api.FormLibraryPurpose, bindings map[string]any,
	vars policy.Vars) (store.LibraryEntry, map[string]any, error) {
	e, err := s.Store.GetLibraryEntry(ctx, key, version)
	if errors.Is(err, store.ErrNotFound) || err == nil && e.Use != string(use) {
		return e, nil, &BadRequestError{Msg: fmt.Sprintf("The form library has no %s %s version %d.", use, key, version)}
	}
	if err != nil {
		return e, nil, err
	}
	params := map[string]any{}
	for _, prm := range libraryParameters(e) {
		expr, _ := bindings[prm.Name].(string)
		var v any
		if strings.TrimSpace(expr) != "" {
			v, _ = policy.EvalSource(expr, vars)
		}
		if v == nil || v == "" {
			if prm.Required != nil && *prm.Required {
				return e, nil, errUnbound
			}
			continue
		}
		params[prm.Name] = v
	}
	return e, params, nil
}

// libraryVars are the CEL variables of a library entry's expressions.
func libraryVars(params map[string]any, vars policy.Vars) policy.Vars {
	out := maps.Clone(vars)
	delete(out, "inputs")
	delete(out, "propgroup")
	out["params"] = params
	return out
}

// checkLibraryRefs checks the library entries a definition names: each
// exists at its version and is what the form uses it as; its parameters
// are the entry's, the required ones given to publish.
func (s *Service) checkLibraryRefs(ctx context.Context, d api.CatalogVersionDefinition, prefix string, publish bool) ([]api.FieldError, error) {
	type ref struct {
		at, key string
		version int
		use     api.FormLibraryPurpose
		params  map[string]any
	}
	var refs []ref
	for _, name := range sortedKeys(d.UiSchema.Fields) {
		src := d.UiSchema.Fields[name].ValueSource
		if src == nil || src.Kind != api.Library || deref(src.Name) == "" || src.Version == nil {
			continue
		}
		refs = append(refs, ref{prefix + "/ui_schema/fields/" + name + "/value_source", *src.Name, *src.Version, api.Source, derefMap(src.Params)})
	}
	for i, v := range validationsOf(d.UiSchema) {
		if v.Kind != api.Library || deref(v.Name) == "" || v.Version == nil {
			continue
		}
		refs = append(refs, ref{fmt.Sprintf("%s/ui_schema/validations/%d", prefix, i), *v.Name, *v.Version, api.Validation, derefMap(v.Params)})
	}
	var errs []api.FieldError
	for _, r := range refs {
		e, err := s.Store.GetLibraryEntry(ctx, r.key, r.version)
		if errors.Is(err, store.ErrNotFound) {
			latest, err := s.Store.GetLibraryEntry(ctx, r.key, 0)
			switch {
			case errors.Is(err, store.ErrNotFound):
				errs = append(errs, fieldErr(r.at+"/name", "unknown", fmt.Sprintf("The form library has no entry %q.", r.key)))
			case err != nil:
				return nil, err
			default:
				errs = append(errs, fieldErr(r.at+"/version", "unknown", fmt.Sprintf("Entry %s has no version %d: its latest is %d.", r.key, r.version, latest.Version)))
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if e.Use != string(r.use) {
			errs = append(errs, fieldErr(r.at+"/name", "invalid", fmt.Sprintf("Entry %s is a %s, not a %s.", r.key, e.Use, r.use)))
			continue
		}
		parameters := libraryParameters(e)
		for _, k := range sortedKeys(r.params) {
			if !slices.ContainsFunc(parameters, func(p api.FormLibraryParameter) bool { return p.Name == k }) {
				errs = append(errs, fieldErr(r.at+"/params/"+k, "unknown", fmt.Sprintf("Entry %s has no parameter %s.", r.key, k)))
			}
		}
		for _, prm := range parameters {
			if expr, _ := r.params[prm.Name].(string); publish && prm.Required != nil && *prm.Required && strings.TrimSpace(expr) == "" {
				errs = append(errs, fieldErr(r.at+"/params/"+prm.Name, "required", fmt.Sprintf("Entry %s needs %s (%s): give it an expression.", r.key, prm.Label, prm.Name)))
			}
		}
	}
	return errs, nil
}

// checkLibraryBindings checks a reference to a library entry as written:
// a key, a version, and a CEL expression per parameter.
func checkLibraryBindings(name *string, version *int, params *map[string]any, ptr string) []api.FieldError {
	var errs []api.FieldError
	if deref(name) == "" {
		errs = append(errs, fieldErr(ptr+"/name", "required", "Name the form library entry."))
	}
	if version == nil || *version < 1 {
		errs = append(errs, fieldErr(ptr+"/version", "required", "Name the entry's version."))
	}
	for _, k := range sortedKeys(derefMap(params)) {
		expr, ok := (*params)[k].(string)
		if !ok {
			errs = append(errs, fieldErr(ptr+"/params/"+k, "invalid", "Give the parameter a CEL expression, for example inputs.environment."))
			continue
		}
		if strings.TrimSpace(expr) == "" {
			continue
		}
		if err := policy.CheckSource(expr); err != nil {
			errs = append(errs, fieldErr(ptr+"/params/"+k, "invalid", "This expression is not valid CEL: "+err.Error()))
		}
	}
	return errs
}

func derefInt(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

func derefMap(m *map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return *m
}

// bumpLibrary has the definition name version of the library entry key
// wherever it names an earlier one, and says whether it changed.
func bumpLibrary(d *api.CatalogVersionDefinition, key string, version int) bool {
	changed := false
	for name, f := range d.UiSchema.Fields {
		if src := f.ValueSource; src != nil && src.Kind == api.Library && deref(src.Name) == key && src.Version != nil && *src.Version < version {
			bumped := *src
			bumped.Version = &version
			f.ValueSource = &bumped
			d.UiSchema.Fields[name] = f
			changed = true
		}
	}
	if d.UiSchema.Validations != nil {
		for i, v := range *d.UiSchema.Validations {
			if v.Kind == api.Library && deref(v.Name) == key && v.Version != nil && *v.Version < version {
				(*d.UiSchema.Validations)[i].Version = &version
				changed = true
			}
		}
	}
	return changed
}

// libraryDrafts gives each item whose draft or published version names an
// earlier version of the entry a draft naming this one: its draft changed,
// or a new one made from its published version.
func (s *Service) libraryDrafts(ctx context.Context, p *auth.Principal, key string, version int) ([]api.FormLibraryDraft, error) {
	uses, err := s.Store.LibraryUses(ctx, key)
	if err != nil {
		return nil, err
	}
	var items []string
	for _, u := range uses {
		if (u.State == "draft" || u.State == "published") && u.EntryVersion < version && !slices.Contains(items, u.ItemID) {
			items = append(items, u.ItemID)
		}
	}
	out := []api.FormLibraryDraft{}
	for _, id := range items {
		d, ok, err := s.libraryDraft(ctx, p, id, key, version)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *Service) libraryDraft(ctx context.Context, p *auth.Principal, itemID, key string, version int) (api.FormLibraryDraft, bool, error) {
	it, err := s.Store.GetAdminItem(ctx, itemID)
	if err != nil {
		return api.FormLibraryDraft{}, false, err
	}
	out := api.FormLibraryDraft{ItemId: it.ID, Slug: it.Slug, Name: it.Name}
	versions, err := s.Store.ItemVersions(ctx, it.ID)
	if err != nil {
		return out, false, err
	}
	var from *store.VersionRow
	for i, v := range versions {
		if v.State == "draft" {
			from = &versions[i]
			break
		}
		if v.State == "published" && from == nil {
			from = &versions[i]
		}
	}
	if from == nil {
		return out, false, nil
	}
	v, err := s.Store.GetVersionAsWritten(ctx, from.ID)
	if err != nil {
		return out, false, err
	}
	def, err := apiDef(v)
	if err != nil {
		return out, false, err
	}
	if !bumpLibrary(&def, key, version) {
		return out, false, nil
	}
	problem := func(msg string) (api.FormLibraryDraft, bool, error) {
		out.Problem = &msg
		return out, true, nil
	}
	errs, err := s.checkVersion(ctx, it.Type, def, "/definition", false)
	if err != nil {
		return out, false, err
	}
	if len(errs) > 0 {
		return problem(fmt.Sprintf("With version %d of %s, its form wouldn't pass the designer's checks: %s", version, key, errs[0].Message))
	}
	if from.State == "draft" {
		out.Number = from.Number
		_, err := s.Store.SaveDraft(ctx, from.ID, nil, nil, storeDef(def), &from.Revision)
		switch {
		case errors.Is(err, store.ErrVersionChanged), errors.Is(err, store.ErrVersionState):
			return problem(fmt.Sprintf("Its draft %s changed at the same time: name version %d of %s in it.", from.Number, version, key))
		case err != nil:
			return out, false, err
		}
		s.auditCatalog(ctx, p, "catalog.version.save", it.ID, map[string]any{"version_id": from.ID, "form_library": key, "entry_version": version})
		return out, true, nil
	}
	out.Number, out.Created = nextNumber(versions), true
	nv := store.NewVersion{ID: ids.New(ids.Version), ItemID: it.ID, Number: out.Number,
		Changelog: fmt.Sprintf("Uses version %d of %s, from the form library.", version, key), CreatedBy: p.User.ID, Def: storeDef(def)}
	switch err := s.Store.CreateDraft(ctx, nv); {
	case errors.Is(err, store.ErrDraftExists), errors.Is(err, store.ErrNumberTaken):
		out.Created = false
		return problem(fmt.Sprintf("A draft was made at the same time: name version %d of %s in it.", version, key))
	case err != nil:
		return out, false, err
	}
	s.auditCatalog(ctx, p, "catalog.version.create", it.ID, map[string]any{"version": out.Number, "from": from.ID,
		"form_library": key, "entry_version": version})
	return out, true, nil
}
