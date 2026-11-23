package blueprint

import (
	"fmt"
	"regexp"
	"strings"
)

// Files in shared variables (ADR-0057): cloud-init user data reads the file
// one holds as {{ file "<group>.<name>" }}. Only the name travels in the
// plan; the worker reads the file when it writes the user data.

var fileVariable = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}\.[a-z][a-z0-9_]{0,40}$`)

// UserDataFiles lists the shared variables whose file user data reads, as
// <group>.<name> in Name, each by its line. One named otherwise than in
// quotes is an error, as for a secret.
func UserDataFiles(text string) ([]SecretRef, error) {
	refs, bad, err := userDataCalls(text, "file")
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		if !fileVariable.MatchString(r.Name) {
			bad = append(bad, fmt.Sprintf("line %d", r.Line))
		}
	}
	if len(bad) > 0 {
		return refs, fmt.Errorf("name the shared variable in quotes, as in {{ file \"group.name\" }}: %s", strings.Join(bad, ", "))
	}
	return refs, nil
}

// Files lists the shared variables whose file a blueprint's user data
// reads, with where.
func (b Blueprint) Files() []SecretRef {
	var refs []SecretRef
	for i, c := range b.ComponentList() {
		if c.Guest == nil || c.Guest.CloudInit == nil {
			continue
		}
		at := "/guest/cloud_init/user_data"
		if b.Multi() {
			at = fmt.Sprintf("/components/%d%s", i, at)
		}
		names, _ := UserDataFiles(c.Guest.CloudInit.UserData)
		for _, r := range names {
			r.Pointer = at
			refs = append(refs, r)
		}
	}
	return dedupeRefs(refs)
}
