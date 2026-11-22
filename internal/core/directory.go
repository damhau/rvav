package core

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/ad"
)

// DirectoryConfig is how a client of the directory the settings name
// connects, trusting these CAs (nil: the process's).
func DirectoryConfig(c config.ADConfig, roots *x509.CertPool) ad.Config {
	o := c.TLS()
	o.Roots = roots
	return ad.Config{URL: c.URL, Username: c.Username, Password: c.Password, BaseDN: c.BaseDN, PlainLDAP: c.PlainLDAP, TLS: o}
}

func testAD(ctx context.Context, c config.ADConfig, roots *x509.CertPool, check checkFunc) {
	if c.URL == "" || c.BaseDN == "" {
		check("Settings", errors.New("give the directory's URL and base DN"), "")
		return
	}
	if strings.HasPrefix(c.URL, "ldap://") && c.PlainLDAP {
		check("Encryption", nil, "None: plain LDAP sends the service account's password and every object in the clear.")
	}
	err := ad.New(DirectoryConfig(c, roots)).Test(ctx)
	if !check("Bind", err, fmt.Sprintf("Bound to %s as %s and read %s.", c.URL, c.Username, c.BaseDN)) {
		return
	}
	check("Domain", nil, "Computer objects get DNS names in "+ad.Domain(c.BaseDN)+" when their VM has none.")
}
