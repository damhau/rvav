package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/store"
)

// PrivateKey is a deployment's generated private key, handed out once.
type PrivateKey struct {
	PEM, Username, Fingerprint string
}

// DownloadPrivateKey gives the deployment's owner the private key rvav
// made for it, and forgets it in the same transaction: a second call, or
// one after the deployment's VMs were deleted, is refused (ADR-0039).
func (s *Service) DownloadPrivateKey(ctx context.Context, p *auth.Principal, deploymentID string) (PrivateKey, error) {
	var out PrivateKey
	d, err := s.visibleDeployment(ctx, p, deploymentID)
	if err != nil {
		return out, err
	}
	if d.OwnerID != p.User.ID {
		return out, fmt.Errorf("%w: only the deployment's owner, %s, can download its private key", ErrForbidden, d.OwnerName)
	}
	if s.Secrets == nil {
		return out, &ConflictError{Msg: "rvav has no secrets key (RVAV_SECRETS_KEY), so it has no private key to give."}
	}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		k, err := s.Store.LockDeploymentKey(ctx, tx, d.ID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		switch {
		case k.DownloadedAt != nil:
			return &ConflictError{Msg: fmt.Sprintf("%s downloaded the private key on %s, and rvav no longer has it.",
				cmp.Or(deref(k.DownloadedByName), "Someone"), k.DownloadedAt.UTC().Format("2 Jan 2006 15:04 UTC"))}
		case k.Ciphertext == nil:
			return &ConflictError{Msg: "rvav no longer has the private key: it was forgotten when the deployment's VMs were deleted."}
		}
		plain, err := s.Secrets.Open(store.DeploymentKeyName(d.ID), deref(k.KeyID), k.Nonce, k.Ciphertext)
		if err != nil {
			return err
		}
		if err := s.Store.ForgetDeploymentKey(ctx, tx, d.ID, &p.User.ID); err != nil {
			return err
		}
		out = PrivateKey{PEM: string(plain), Username: k.Username, Fingerprint: k.Fingerprint}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "deployment.private_key.download",
			TargetType: "deployment", TargetID: d.ID, ProjectID: &d.ProjectID, Detail: map[string]any{"fingerprint": k.Fingerprint}})
	})
	if err != nil {
		return PrivateKey{}, err
	}
	if s.Events != nil {
		s.Events.Deployment(ctx, d.ID)
	}
	return out, nil
}

// accessProblem says why the guest's remote access can't be given on the
// template the form chose, or "": a catalogue template sets the OS family
// and may name a customization specification (ADR-0037).
func accessProblem(g blueprint.Guest, image *store.VMTemplate) string {
	if g.RemoteAccess == nil {
		return ""
	}
	label := "The template"
	if image != nil {
		label = image.Label
	}
	switch {
	case g.OSFamily == "windows":
		return label + " is a Windows template, and this item's remote access is for Linux VMs. Ask a catalog admin to check this item."
	case g.CustomizationSpec != "":
		return fmt.Sprintf("%s configures its VMs with the customization specification %s, and this item's remote access needs cloud-init. Ask a catalog admin to check this item.",
			label, g.CustomizationSpec)
	}
	return ""
}

// checkRemoteAccess checks the user people sign in to the VMs as
// (ADR-0039), said once where the blueprint or a component sets it.
func checkRemoteAccess(b blueprint.Blueprint, fields map[string]api.UiField, ptr string) []api.FieldError {
	var errs []api.FieldError
	said := map[string]bool{}
	add := func(at, code, msg string) {
		if !said[at+msg] {
			said[at+msg] = true
			errs = append(errs, fieldErr(at, code, msg))
		}
	}
	secretInput := func(name string) bool {
		f, ok := fields[name]
		return ok && (f.Secret != nil && *f.Secret || f.Widget == api.UiWidgetSecret)
	}
	for i, c := range b.ComponentList() {
		ra := c.Guest.RemoteAccess
		if ra == nil {
			continue
		}
		at := ptr + "/guest/remote_access"
		if b.Multi() && b.Components[i].Guest != nil && b.Components[i].Guest.RemoteAccess != nil {
			at = fmt.Sprintf("%s/components/%d/guest/remote_access", ptr, i)
		}
		windows := c.Guest.OSFamily == "windows"
		switch ra.Kind {
		case blueprint.AccessSSHKey, blueprint.AccessGeneratedKey, blueprint.AccessPassword:
		default:
			add(at+"/kind", "enum", "ssh_key, generated_key or password.")
		}
		if ra.Username == "" {
			add(at+"/username", "required", "Name the user people sign in as, for example ops.")
		} else if msg := blueprint.CheckUsername(c.Guest.OSFamily, ra.Username); msg != "" {
			add(at+"/username", "invalid", msg)
		}
		switch ra.Kind {
		case blueprint.AccessSSHKey:
			switch {
			case ra.PublicKey == "" && ra.Secret == "":
				add(at+"/public_key", "required", "Give the public key, an input that holds it (${inputs.ssh_key}), or a stored secret (secret).")
			case ra.PublicKey != "" && ra.Secret != "":
				add(at+"/secret", "invalid", "The key comes from public_key or from a stored secret, not both.")
			case ra.PublicKey != "":
				if name, ok := blueprint.AccessInput(ra.PublicKey); ok {
					if _, known := fields[name]; !known {
						add(at+"/public_key", "unknown_input", "There is no input "+name+".")
					}
				} else if strings.Contains(ra.PublicKey, "${") {
					add(at+"/public_key", "invalid", "Read the key from one input, as ${inputs.ssh_key}, or write the key itself.")
				} else if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ra.PublicKey)); err != nil {
					add(at+"/public_key", "invalid", "This is not an SSH public key, as in ssh-ed25519 AAAA… user@host.")
				}
			}
			if ra.Password != "" {
				add(at+"/password", "invalid", "People sign in with the key: leave out the password.")
			}
		case blueprint.AccessGeneratedKey:
			for _, f := range []struct{ name, v string }{{"public_key", ra.PublicKey}, {"password", ra.Password}, {"secret", ra.Secret}} {
				if f.v != "" {
					add(at+"/"+f.name, "invalid", "rvav makes the key pair for each deployment: leave out "+f.name+".")
				}
			}
		case blueprint.AccessPassword:
			switch {
			case ra.Password == "" && ra.Secret == "":
				add(at+"/password", "required", "Read the password from a secret input (${inputs.password}), or name a stored secret (secret).")
			case ra.Password != "" && ra.Secret != "":
				add(at+"/secret", "invalid", "The password comes from a secret input or from a stored secret, not both.")
			case ra.Password != "":
				name, ok := blueprint.AccessInput(ra.Password)
				if !ok || !secretInput(name) {
					add(at+"/password", "invalid", "Read the password from a secret input, as ${inputs.password}: any other is kept with the request.")
				}
			}
			if ra.PublicKey != "" {
				add(at+"/public_key", "invalid", "People sign in with the password: leave out the public key.")
			}
		}
		if windows {
			add(at, "invalid", "Remote access is for Linux VMs: a Windows VM's administrator password is admin_password.")
		} else if c.Guest.CustomizationSpec != "" {
			add(at, "invalid", "Remote access on Linux goes through cloud-init, which doesn't use a customization specification.")
		}
	}
	return errs
}
