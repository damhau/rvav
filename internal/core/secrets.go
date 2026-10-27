package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
)

// Secrets for value sources (ADR-0019): catalog admins and administrators
// store them; their values are never returned.

var secretName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

func (s *Service) presentSecret(ctx context.Context, v store.Secret) (api.Secret, error) {
	users, err := s.Store.SecretUsers(ctx, v.Name)
	out := api.Secret{Name: v.Name, Kind: api.SecretKind(v.Kind), Description: v.Description, CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt, UsedBy: &users}
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
	if !canAdminCatalog(p) {
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
	return out, nil
}

// PutSecret stores a secret, or replaces one (name in the path).
func (s *Service) PutSecret(ctx context.Context, p *auth.Principal, name string, w api.SecretWrite, create bool) (api.Secret, error) {
	if !canAdminCatalog(p) {
		return api.Secret{}, ErrForbidden
	}
	if create {
		name = w.Name
	}
	var errs []api.FieldError
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
	nonce, ct, err := s.Secrets.Seal(name, []byte(deref(w.Value)))
	if errors.Is(err, secrets.ErrNoKey) {
		return api.Secret{}, &ConflictError{Msg: "RVAV_SECRETS_KEY is not set, so rvav can't store secrets."}
	}
	if err != nil {
		return api.Secret{}, err
	}
	row := store.Secret{Name: name, Kind: string(w.Kind), Description: deref(w.Description), Username: deref(w.Username),
		Header: deref(w.Header), Nonce: nonce, Ciphertext: ct}
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
	s.auditCatalog(ctx, p, action, name, map[string]any{"kind": row.Kind}) // never the value
	v, err := s.Store.GetSecret(ctx, name)
	if err != nil {
		return api.Secret{}, err
	}
	return s.presentSecret(ctx, v)
}

// DeleteSecret deletes a secret no value source uses.
func (s *Service) DeleteSecret(ctx context.Context, p *auth.Principal, name string) error {
	if !canAdminCatalog(p) {
		return ErrForbidden
	}
	users, err := s.Store.SecretUsers(ctx, name)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return &ConflictError{Msg: "Items use this secret: " + strings.Join(users, ", ") + "."}
	}
	if err := s.Store.DeleteSecret(ctx, name); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.auditCatalog(ctx, p, "secret.delete", name, nil)
	return nil
}

// secretValue opens a stored secret.
func (s *Service) secretValue(ctx context.Context, name string) (store.Secret, string, error) {
	v, err := s.Store.GetSecret(ctx, name)
	if err != nil {
		return v, "", err
	}
	plain, err := s.Secrets.Open(v.Name, v.Nonce, v.Ciphertext)
	return v, string(plain), err
}
