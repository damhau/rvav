package blueprint

import (
	"regexp"
	"strings"
)

// RemoteAccess is how people sign in to a VM (ADR-0039): a user with an
// SSH key given or generated per deployment, or with a password. Only
// names travel in the plan: an input's or a stored secret's.
type RemoteAccess struct {
	// Kind is ssh_key, generated_key or password.
	Kind     string `json:"kind"`
	Username string `json:"username"`
	// PublicKey is an ssh_key's key: the key itself, or ${inputs.<name>}.
	PublicKey string `json:"public_key,omitempty"`
	// Password is a password's ${inputs.<name>}, a secret input.
	Password string `json:"password,omitempty"`
	// Secret names the stored secret holding an ssh_key's public key, or a
	// password.
	Secret string `json:"secret,omitempty"`
}

const (
	AccessSSHKey       = "ssh_key"
	AccessGeneratedKey = "generated_key"
	AccessPassword     = "password"
)

// UsesKey says whether people sign in with a key rather than a password.
func (r RemoteAccess) UsesKey() bool { return r.Kind == AccessSSHKey || r.Kind == AccessGeneratedKey }

var accessInput = regexp.MustCompile(`^\$\{\s*inputs\.([A-Za-z_][A-Za-z0-9_]*)\s*\}$`)

// AccessInput gives the input a remote access value names, when the whole
// value is ${inputs.<name>}.
func AccessInput(s string) (string, bool) {
	m := accessInput.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	return m[1], true
}

var (
	linuxUser   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	windowsUser = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,19}$`)
)

// CheckUsername says what is wrong with a user name for the OS family, or
// "".
func CheckUsername(osFamily, name string) string {
	if osFamily == "windows" {
		if !windowsUser.MatchString(name) {
			return "A Windows user name of up to 20 letters, digits, dots, dashes and underscores."
		}
		return ""
	}
	switch {
	case name == "root":
		return "Not root: rvav adds a user who signs in, then uses sudo."
	case !linuxUser.MatchString(name):
		return "A Linux user name of up to 32 lower-case letters, digits, dashes and underscores, starting with a letter."
	}
	return ""
}
