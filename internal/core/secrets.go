package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

// Secrets for value sources (ADR-0019), guests, cloud-init and AWX steps
// (ADR-0038): catalog admins and administrators store them, global or a
// project's own; their values are never returned.

var secretName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

func (s *Service) presentSecret(ctx context.Context, v store.Secret) (api.Secret, error) {
	users, err := s.secretUsers(ctx, v.Name)
	out := api.Secret{Name: v.Name, ProjectId: v.ProjectID, Kind: api.SecretKind(v.Kind), Description: v.Description,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, UsedBy: &users}
	if v.Username != "" {
		out.Username = &v.Username
	}
	if v.Header != "" {
		out.Header = &v.Header
	}
	if users == nil {
		out.UsedBy = &[]string{}
	}
	return out, err
}

// ListSecrets lists the stored secrets, without their values.
func (s *Service) ListSecrets(ctx context.Context, p *auth.Principal) (api.SecretList, error) {
	out := api.SecretList{Items: []api.Secret{}}
	if !p.Everywhere(auth.CatalogManage) {
		return out, ErrForbidden
	}
	list, err := s.Store.Secrets(ctx)
	if err != nil {
		return out, err
	}
	for _, v := range list {
		ps, err := s.presentSecret(ctx, v)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, ps)
	}
	all, err := s.Store.ListProjects(ctx, nil, 500, nil)
	if err != nil {
		return out, err
	}
	projects := make([]struct {
		Code string `json:"code"`
		Id   string `json:"id"`
		Name string `json:"name"`
	}, len(all.Items))
	for i, p := range all.Items {
		projects[i].Code, projects[i].Id, projects[i].Name = p.Code, p.ID, p.Name
	}
	out.Projects = &projects
	return out, nil
}

// PutSecret stores a secret, global or a project's, or replaces one (its
// name and project in the path and query).
func (s *Service) PutSecret(ctx context.Context, p *auth.Principal, name, project string, w api.SecretWrite, create bool) (api.Secret, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.Secret{}, ErrForbidden
	}
	if create {
		name, project = w.Name, deref(w.ProjectId)
	}
	var errs []api.FieldError
	var projectID *string
	if project != "" {
		switch proj, err := s.Store.GetProject(ctx, project); {
		case errors.Is(err, store.ErrNotFound) && create:
			errs = append(errs, fieldErr("/project_id", "unknown", fmt.Sprintf("There is no project %q.", project)))
		case errors.Is(err, store.ErrNotFound):
			return api.Secret{}, ErrNotFound
		case err != nil:
			return api.Secret{}, err
		default:
			projectID = &proj.ID
		}
	}
	if !secretName.MatchString(name) || !create && w.Name != "" && w.Name != name {
		errs = append(errs, fieldErr("/name", "pattern", "Lower case letters, digits and dashes, starting with a letter."))
	}
	if strings.TrimSpace(deref(w.Value)) == "" {
		errs = append(errs, fieldErr("/value", "required", "Give the secret's value."))
	}
	switch w.Kind {
	case api.SecretWriteKindBasic:
		if deref(w.Username) == "" {
			errs = append(errs, fieldErr("/username", "required", "A basic secret has a user name."))
		}
	case api.SecretWriteKindHeader:
		if deref(w.Header) == "" || strings.ContainsAny(deref(w.Header), " :\r\n") {
			errs = append(errs, fieldErr("/header", "required", "Name the header, for example X-Api-Key."))
		}
	case api.SecretWriteKindToken:
	default:
		errs = append(errs, fieldErr("/kind", "invalid", "A secret is a token, basic or header."))
	}
	if len(errs) > 0 {
		return api.Secret{}, &ValidationError{Errors: errs}
	}
	row := store.Secret{Name: name, ProjectID: projectID, Kind: string(w.Kind), Description: deref(w.Description),
		Username: deref(w.Username), Header: deref(w.Header)}
	var err error
	row.KeyID, row.Nonce, row.Ciphertext, err = s.Secrets.Seal(row.SealName(), []byte(deref(w.Value)))
	if errors.Is(err, secrets.ErrNoKey) {
		return api.Secret{}, &ConflictError{Msg: "RVAV_SECRETS_KEY is not set, so rvav can't store secrets."}
	}
	if err != nil {
		return api.Secret{}, err
	}
	switch err := s.Store.PutSecret(ctx, row, create); {
	case errors.Is(err, store.ErrInUse):
		return api.Secret{}, &ConflictError{Msg: fmt.Sprintf("The secret %q exists: replace its value instead.", name)}
	case errors.Is(err, store.ErrNotFound):
		return api.Secret{}, ErrNotFound
	case err != nil:
		return api.Secret{}, err
	}
	action := "secret.update"
	if create {
		action = "secret.create"
	}
	s.REST.Forget()                            // answers fetched with the old value
	detail := map[string]any{"kind": row.Kind} // never the value
	if projectID != nil {
		detail["project_id"] = *projectID
	}
	s.auditCatalog(ctx, p, action, name, detail)
	v, err := s.Store.ScopedSecret(ctx, deref(projectID), name)
	if err != nil {
		return api.Secret{}, err
	}
	return s.presentSecret(ctx, v)
}

// DeleteSecret deletes a secret nothing uses, global or a project's (in
// project). A project's own may go while used when a global secret of the
// same name remains: its requests open that one instead.
func (s *Service) DeleteSecret(ctx context.Context, p *auth.Principal, name, project string) error {
	if !p.Everywhere(auth.CatalogManage) {
		return ErrForbidden
	}
	v, err := s.Store.ScopedSecret(ctx, project, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	fallback := false
	if v.ProjectID != nil {
		switch _, err := s.Store.GetSecret(ctx, name); {
		case err == nil:
			fallback = true
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
	}
	if !fallback {
		users, err := s.secretUsers(ctx, name)
		if err != nil {
			return err
		}
		if len(users) > 0 {
			return &ConflictError{Msg: "Items or templates use this secret: " + strings.Join(users, ", ") + "."}
		}
	}
	if err := s.Store.DeleteSecret(ctx, project, name); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.REST.Forget()
	var detail map[string]any
	if v.ProjectID != nil {
		detail = map[string]any{"project_id": *v.ProjectID}
	}
	s.auditCatalog(ctx, p, "secret.delete", name, detail)
	return nil
}

// secretUsers lists what uses a secret: the slugs of items whose versions
// name it (in a REST value source or call, a Windows guest, user data or an
// AWX step's binding), and template:<key> for templates whose user data
// reads it.
func (s *Service) secretUsers(ctx context.Context, name string) ([]string, error) {
	users, err := s.Store.SecretUsers(ctx, name)
	if err != nil {
		return nil, err
	}
	versions, err := s.Store.BlueprintsMentioning(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, v := range versions {
		var b blueprint.Blueprint
		if json.Unmarshal(v.Blueprint, &b) != nil {
			continue
		}
		if slices.ContainsFunc(b.Secrets(), func(r blueprint.SecretRef) bool { return r.Name == name }) {
			users = append(users, v.Slug)
		}
	}
	templates, err := s.Store.VMTemplates(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range templates {
		refs, _ := blueprint.UserDataSecrets(t.UserData)
		if slices.ContainsFunc(refs, func(r blueprint.SecretRef) bool { return r.Name == name }) {
			users = append(users, "template:"+t.Key)
		}
	}
	slices.Sort(users)
	return slices.Compact(users), nil
}

// secretValue opens the stored secret a project's forms and requests read
// by this name: its own, else the global one.
func (s *Service) secretValue(ctx context.Context, project, name string) (store.Secret, string, error) {
	v, err := s.Store.SecretFor(ctx, project, name)
	if err != nil {
		return v, "", err
	}
	plain, err := s.Secrets.Open(v.SealName(), v.KeyID, v.Nonce, v.Ciphertext)
	return v, string(plain), err
}

// anySecret gives a secret of this name, global or a project's: what an
// item may name when saved. A request checks that its project has one
// (evaluateSecrets).
func (s *Service) anySecret(ctx context.Context, name string) (store.Secret, error) {
	list, err := s.Store.SecretsNamed(ctx, name)
	if err != nil || len(list) == 0 {
		return store.Secret{}, cmp.Or(err, store.ErrNotFound)
	}
	return list[0], nil
}

// evaluateSecrets checks the request's project has each stored secret its
// guests, user data and AWX steps open (REST calls are checked with their
// step).
func (s *Service) evaluateSecrets(ctx context.Context, ev *Evaluation) error {
	var names []string
	for _, r := range ev.Blueprint.Secrets() {
		names = append(names, r.Name)
	}
	for _, part := range ev.Parts {
		if part.image != nil {
			refs, _ := blueprint.UserDataSecrets(part.image.UserData)
			for _, r := range refs {
				names = append(names, r.Name)
			}
		}
	}
	slices.Sort(names)
	for _, name := range slices.Compact(names) {
		if _, err := s.Store.SecretFor(ctx, ev.Project.ID, name); errors.Is(err, store.ErrNotFound) {
			ev.Errors = append(ev.Errors, fieldErr("", "not_available", fmt.Sprintf(
				"%s opens the secret %q, which %s doesn't have. Ask a catalog admin to check this item.", ev.Item.Name, name, ev.Project.Name)))
		} else if err != nil {
			return err
		}
	}
	return nil
}
