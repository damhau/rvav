package provision

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// access is a VM's remote access as the worker works it out (ADR-0039):
// the password opened here goes into the guest's configuration only,
// never into a result, a step or a log.
type access struct {
	kind        string
	username    string
	publicKey   string
	fingerprint string
	password    string
}

// remoteAccess works out the guest's remote access: the key from its
// input, its stored secret or the deployment's key pair, the password
// from its secret input or stored secret. inputs are the request's, its
// secret inputs opened.
func (a *Activities) remoteAccess(ctx context.Context, in VMInput, inputs map[string]any) (*access, error) {
	ra := in.Guest.RemoteAccess
	if ra == nil {
		return nil, nil
	}
	out := &access{kind: ra.Kind, username: ra.Username}
	fromInput := func(v string) (string, string) {
		name, ok := blueprint.AccessInput(v)
		if !ok {
			return v, ""
		}
		s, _ := inputs[name].(string)
		return s, name
	}
	switch ra.Kind {
	case blueprint.AccessSSHKey:
		key, input := fromInput(ra.PublicKey)
		where := "remote_access.public_key"
		if input != "" {
			where = "input " + input
		}
		if ra.Secret != "" {
			_, v, err := a.openSecret(ctx, in.ProjectID, ra.Secret)
			if err != nil {
				return nil, err
			}
			key, where = v, "secret "+ra.Secret
		}
		pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
		if err != nil {
			return nil, permanent("The SSH public key from %s is not one (ssh-ed25519 AAAA… user@host)", where)
		}
		out.publicKey, out.fingerprint = authorizedKey(pub, comment), ssh.FingerprintSHA256(pub)
	case blueprint.AccessGeneratedKey:
		k, err := a.deploymentKey(ctx, in.VM.DeploymentID, ra.Username)
		if err != nil {
			return nil, err
		}
		out.publicKey, out.fingerprint = k.PublicKey, k.Fingerprint
	case blueprint.AccessPassword:
		pw, input := fromInput(ra.Password)
		if ra.Secret != "" {
			_, v, err := a.openSecret(ctx, in.ProjectID, ra.Secret)
			if err != nil {
				return nil, err
			}
			pw = v
		}
		if pw == "" {
			return nil, permanent("No password for %s: input %s is empty", ra.Username, input)
		}
		out.password = pw
	default:
		return nil, permanent("Remote access of kind %q: ask a catalog admin to check this item", ra.Kind)
	}
	return out, nil
}

func authorizedKey(pub ssh.PublicKey, comment string) string {
	s := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		s += " " + comment
	}
	return s
}

// deploymentKey gives the deployment's key pair, made the first time one
// of its VMs needs it: an Ed25519 key whose private half is sealed at
// once and leaves the worker only when its owner downloads it.
func (a *Activities) deploymentKey(ctx context.Context, deploymentID, username string) (store.DeploymentKey, error) {
	k, err := a.Store.DeploymentKey(ctx, a.Store.Pool, deploymentID)
	if !errors.Is(err, store.ErrNotFound) {
		return k, err
	}
	if a.Secrets == nil {
		return k, permanent("rvav has no secrets key (RVAV_SECRETS_KEY), so it can't keep a generated private key")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return k, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return k, err
	}
	comment := "rvav-" + deploymentID
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return k, err
	}
	keyID, nonce, ct, err := a.Secrets.Seal(store.DeploymentKeyName(deploymentID), pem.EncodeToMemory(block))
	if err != nil {
		return k, err
	}
	return a.Store.KeepDeploymentKey(ctx, store.DeploymentKey{DeploymentID: deploymentID, Username: username,
		PublicKey: authorizedKey(sshPub, comment), Fingerprint: ssh.FingerprintSHA256(sshPub), KeyID: &keyID, Nonce: nonce, Ciphertext: ct})
}

// cloudConfig is the access as cloud-init's users module makes it: the
// user, with sudo, and its key or password.
func (r *access) cloudConfig() map[string]any {
	user := map[string]any{"name": r.username, "shell": "/bin/bash", "sudo": "ALL=(ALL) NOPASSWD:ALL"}
	out := map[string]any{"users": []any{user}}
	if r.publicKey != "" {
		user["ssh_authorized_keys"] = []any{r.publicKey}
		user["lock_passwd"] = true
	} else {
		user["plain_text_passwd"], user["lock_passwd"] = r.password, false
		out["ssh_pwauth"] = true
	}
	return out
}

// userData is the VM's user data: the template's and the item's merged
// (mergeUserData), then the remote access's user added. The distribution's
// default user stays when neither listed users: cloud-init makes it only
// when no document does.
func userData(image, own string, acc *access) (string, error) {
	if acc == nil {
		return mergeUserData(image, own)
	}
	var docs []string
	for _, d := range []string{image, own} {
		if d != "" {
			docs = append(docs, d)
		}
	}
	doc := acc.cloudConfig()
	listed := false
	configs := map[string]any{}
	for _, d := range docs {
		if !isCloudConfig(d) {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal([]byte(d), &m); err != nil {
			return "", fmt.Errorf("not a YAML map: %v", err)
		}
		if _, ok := m["users"]; ok {
			listed = true
		}
		configs = mergeConfig(configs, m)
	}
	if !listed {
		doc["users"] = append([]any{"default"}, doc["users"].([]any)...)
	}
	if !slices.ContainsFunc(docs, func(d string) bool { return !isCloudConfig(d) }) {
		out, err := yaml.Marshal(mergeConfig(configs, doc))
		if err != nil {
			return "", err
		}
		return "#cloud-config\n" + string(out), nil
	}
	// A part of its own, merged into what the earlier parts set: users
	// appended.
	doc["merge_how"] = "list(append)+dict(no_replace,recurse_list)+str()"
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return multipartUserData(append(docs, "#cloud-config\n"+string(out))...)
}

// describe says it for a step's detail: the user and the key's
// fingerprint, never the password.
func (r *access) describe() string {
	switch r.kind {
	case blueprint.AccessPassword:
		return "user " + r.username + " with a password"
	case blueprint.AccessGeneratedKey:
		return "user " + r.username + " with the deployment's key " + r.fingerprint
	}
	return "user " + r.username + " with key " + r.fingerprint
}

// withCleanup has cloud-init clear the user data from guestinfo once it
// read it (cleanup-guestinfo): it holds a password or another secret.
func withCleanup(meta []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		return nil, err
	}
	m["cleanup-guestinfo"] = []string{"userdata"}
	return json.Marshal(m)
}

// recordAccess records who signs in to the component's VMs, and how: the
// deployment page shows it (ADR-0039). Nothing secret is recorded.
func (a *Activities) recordAccess(ctx context.Context, in VMInput, acc *access) error {
	if acc == nil {
		return nil
	}
	component := in.VM.Component
	if component == "" {
		component = blueprint.SingleKey
	}
	props, _ := json.Marshal(map[string]any{"component": component, "kind": acc.kind, "username": acc.username,
		"fingerprint": acc.fingerprint})
	ext := in.VM.DeploymentID + "/" + component
	_, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.VM.DeploymentID,
		RequestID: &in.RequestID, Type: AccessResource, Name: component, ExternalID: &ext, Properties: props})
	return err
}

// AccessResource is the resource type recording a component's remote
// access.
const AccessResource = "guest.access"
