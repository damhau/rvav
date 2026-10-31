// Package buildinfo says which rvav this is.
package buildinfo

import "runtime/debug"

// version is set at build time (-ldflags "-X .../buildinfo.version=1.2.0").
var version string

// Version is the release built, else the commit it was built from, else
// "dev".
func Version() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		var rev string
		dirty := false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if rev != "" && dirty {
			return rev + "+dirty"
		}
		if rev != "" {
			return rev
		}
	}
	return "dev"
}
