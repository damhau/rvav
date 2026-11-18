package blueprint

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/restcall"
)

// Custom properties and folders of projects, blueprints and components
// (ADR-0038).

// MaxProperties is the most custom properties a project or a component has.
const MaxProperties = 50

var propertyKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)

// CheckProperty says what is wrong with a custom property, "" when nothing:
// its key names a vSphere custom attribute.
func CheckProperty(key, value string) string {
	switch {
	case !propertyKey.MatchString(key):
		return "A key of up to 64 letters, digits, '.', '_' and '-', starting with a letter."
	case len(value) > 512:
		return "A value of up to 512 characters."
	}
	return ""
}

// CheckFolder says what is wrong with a component's folder, "" when
// nothing.
func CheckFolder(folder string) string {
	if slices.Contains(strings.Split(folder, "/"), "..") {
		return "A folder without '..': a path below the datacenter's VM folder, or one from the root such as /GVA/vm/rvav/web."
	}
	if _, err := restcall.ParseURL(folder); err != nil {
		return "The folder's ${...} is not closed or is empty."
	}
	return ""
}

// FolderUnder is where a VM goes when its component names a folder: a path
// from the root as it is, a relative one below the VM folder of the
// placement rule's datacenter (/GVA/vm for /GVA/vm/rvav/web), or below the
// rule's folder when it isn't in a datacenter's VM folder.
func FolderUnder(ruleFolder, folder string) string {
	folder = strings.TrimSpace(folder)
	switch {
	case strings.Trim(folder, "/") == "":
		return ruleFolder
	case strings.HasPrefix(folder, "/"):
		return path.Clean(folder)
	}
	root := path.Clean("/" + strings.Trim(ruleFolder, "/"))
	if parts := strings.Split(strings.Trim(ruleFolder, "/"), "/"); len(parts) >= 2 && parts[1] == "vm" {
		root = "/" + parts[0] + "/vm"
	}
	return path.Join(root, folder)
}

// Interpolate works out text holding ${CEL}: each expression gives text, a
// number or a boolean.
func Interpolate(text string, vars policy.Vars) (string, error) {
	if !HasExpr(text) {
		return text, nil
	}
	parts, err := restcall.ParseURL(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Expr == "" {
			b.WriteString(p.Text)
			continue
		}
		v, err := policy.Eval(p.Expr, vars)
		if err != nil {
			return "", err
		}
		switch v.(type) {
		case string, int64, float64, bool:
		default:
			return "", errNotScalar
		}
		b.WriteString(fmt.Sprint(v))
	}
	return b.String(), nil
}

var errNotScalar = errors.New("gives no text")

// mergeProperties are under's properties with over's on top.
func mergeProperties(under, over map[string]string) map[string]string {
	if len(under) == 0 && len(over) == 0 {
		return nil
	}
	out := make(map[string]string, len(under)+len(over))
	for k, v := range under {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}
