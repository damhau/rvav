// Package ids creates the opaque, time-ordered identifiers used across rvav,
// for example "req_01jb8zq3m4k2x7v9c0d5e6f7g8".
package ids

import (
	"crypto/rand"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	Request    = "req"
	Step       = "stp"
	Deployment = "dep"
	Resource   = "res"
	Item       = "itm"
	Version    = "ver"
	Project    = "prj"
	User       = "usr"
	Policy     = "pol"
	Placement  = "plc"
	Token      = "tok"
	Approval   = "apr"
)

// New returns prefix + "_" + a lower-case ULID.
func New(prefix string) string {
	id := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader)
	return prefix + "_" + strings.ToLower(id.String())
}
