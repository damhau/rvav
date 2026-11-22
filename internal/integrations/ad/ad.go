// Package ad is rvav's Active Directory (ADR-0064): the computer object
// it makes for a VM before the VM is built, in the OU its project or
// environment names, and deletes with the VM. It speaks LDAP over TLS,
// LDAPS or StartTLS, bound as a service account; plain LDAP only when an
// administrator allowed it.
package ad

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/go-ldap/ldap/v3"

	"github.com/damhau/rvav/internal/trust"
)

// MaxName is the longest computer name: a NetBIOS name, which Windows'
// domain join and adcli use as the account's sAMAccountName (NAME$).
const MaxName = 15

// UserAccountControl is a computer object's: a workstation or member
// server's trust account (WORKSTATION_TRUST_ACCOUNT), enabled, its
// password required.
const UserAccountControl = "4096"

// Config is where the directory is and how rvav binds there.
type Config struct {
	// URL is ldaps://host[:636], or ldap://host[:389] for StartTLS.
	URL      string
	Username string
	Password string
	// BaseDN is the domain's naming context, or the part of it rvav
	// searches: OUs are under it.
	BaseDN string
	// PlainLDAP sends ldap:// in the clear, the password too: never unless
	// an administrator ticked it.
	PlainLDAP bool
	TLS       trust.Options
}

// Configured says whether rvav has a directory.
func (c Config) Configured() bool { return c.URL != "" && c.BaseDN != "" }

// Computer is a computer object.
type Computer struct {
	// Name is the computer's name, without the $ of its sAMAccountName.
	Name        string
	DN          string
	DNSHostName string
	Description string
}

// ErrExists says a computer object of that name is in the directory.
var ErrExists = errors.New("a computer object of that name exists")

// Client is the directory: each call binds, does its work and unbinds.
type Client struct{ cfg Config }

// New is a client of the directory the configuration names.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

// Domain is the directory's DNS domain: its base DN's DC parts.
func (c *Client) Domain() string { return Domain(c.cfg.BaseDN) }

// BaseDN is where it searches.
func (c *Client) BaseDN() string { return c.cfg.BaseDN }

const timeout = 30 * time.Second

// dial connects and binds. The connection closes when ctx ends.
func (c *Client) dial(ctx context.Context) (*ldap.Conn, func(), error) {
	u, err := url.Parse(c.cfg.URL)
	if err != nil || u.Hostname() == "" {
		return nil, nil, fmt.Errorf("%q is not an LDAP URL", c.cfg.URL)
	}
	host := u.Hostname()
	o := c.cfg.TLS
	if o.Host == "" {
		o.Host = host
	}
	tc := trust.Config(o)
	tc.ServerName = host
	d := &net.Dialer{Timeout: 10 * time.Second}
	var conn *ldap.Conn
	switch u.Scheme {
	case "ldaps":
		conn, err = ldap.DialURL(c.cfg.URL, ldap.DialWithDialer(d), ldap.DialWithTLSConfig(tc))
	case "ldap":
		conn, err = ldap.DialURL(c.cfg.URL, ldap.DialWithDialer(d))
		if err == nil && !c.cfg.PlainLDAP {
			if err = conn.StartTLS(tc); err != nil {
				conn.Close()
				err = fmt.Errorf("StartTLS: %w", err)
			}
		}
	default:
		return nil, nil, fmt.Errorf("%q: ldaps:// or ldap://", c.cfg.URL)
	}
	if err != nil {
		return nil, nil, err
	}
	conn.SetTimeout(timeout)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	closeAll := func() { stop(); conn.Close() }
	if err := conn.Bind(c.cfg.Username, c.cfg.Password); err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("bind as %s: %w", c.cfg.Username, err)
	}
	return conn, closeAll, nil
}

// Test binds and reads the base DN, saying what it found.
func (c *Client) Test(ctx context.Context) error {
	conn, done, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer done()
	_, err = conn.Search(ldap.NewSearchRequest(c.cfg.BaseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 30, false,
		"(objectClass=*)", []string{"distinguishedName"}, nil))
	if err != nil {
		return fmt.Errorf("read %s: %w", c.cfg.BaseDN, err)
	}
	return nil
}

// Exists says whether an entry is there: an OU or a group, checked when
// an administrator names one.
func (c *Client) Exists(ctx context.Context, dn string) (bool, error) {
	conn, done, err := c.dial(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	_, err = conn.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 30, false,
		"(objectClass=*)", []string{"distinguishedName"}, nil))
	if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		return false, nil
	}
	return err == nil, err
}

// Find is the object whose sAMAccountName is the computer's (NAME$),
// anywhere in the domain, under the base DN or not: names are unique in a
// domain, whatever the OU. Nil when there is none.
func (c *Client) Find(ctx context.Context, name string) (*Computer, error) {
	conn, done, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return find(conn, c.cfg.BaseDN, name)
}

func find(conn *ldap.Conn, base, name string) (*Computer, error) {
	res, err := conn.Search(ldap.NewSearchRequest(DomainDN(base), ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 30, false,
		"(sAMAccountName="+ldap.EscapeFilter(Account(name))+")", []string{"dNSHostName", "description"}, nil))
	if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		return nil, err
	}
	if res == nil || len(res.Entries) == 0 {
		return nil, nil
	}
	e := res.Entries[0]
	return &Computer{Name: name, DN: e.DN, DNSHostName: e.GetAttributeValue("dNSHostName"),
		Description: e.GetAttributeValue("description")}, nil
}

// Create makes the computer object, then adds it to the groups. An object
// at that DN already made by this same call before (its description the
// same) is the one made: a retried activity doesn't make it twice.
//
// Over TLS, the object gets a random password no one is told: enabled,
// with a password a domain's policy accepts, it can't be signed in to
// until a domain join, whose account resets it, takes it. Active
// Directory sets a password only over an encrypted connection: over plain
// LDAP the object has none, which a domain controller whose policy needs
// one refuses.
func (c *Client) Create(ctx context.Context, comp Computer, groups []string) error {
	conn, done, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer done()
	add := ldap.NewAddRequest(comp.DN, nil)
	add.Attribute("objectClass", []string{"top", "person", "organizationalPerson", "user", "computer"})
	add.Attribute("cn", []string{strings.ToUpper(comp.Name)})
	add.Attribute("sAMAccountName", []string{Account(comp.Name)})
	add.Attribute("userAccountControl", []string{UserAccountControl})
	if comp.DNSHostName != "" {
		add.Attribute("dNSHostName", []string{comp.DNSHostName})
	}
	if comp.Description != "" {
		add.Attribute("description", []string{comp.Description})
	}
	if !c.cfg.PlainLDAP {
		pw, err := password()
		if err != nil {
			return err
		}
		add.Attribute("unicodePwd", []string{pw})
	}
	err = conn.Add(add)
	if exists(err) {
		found, ferr := find(conn, c.cfg.BaseDN, comp.Name)
		if ferr != nil {
			return ferr
		}
		if found == nil || !SameDN(found.DN, comp.DN) || found.Description != comp.Description {
			return fmt.Errorf("%w: %s", ErrExists, Account(comp.Name))
		}
		err = nil
	}
	if err != nil {
		return fmt.Errorf("add %s: %w", comp.DN, err)
	}
	for _, g := range groups {
		mod := ldap.NewModifyRequest(g, nil)
		mod.Add("member", []string{comp.DN})
		// Active Directory says a member already there exists (68); others
		// that the value does (20).
		if err := conn.Modify(mod); err != nil && !exists(err) && !ldap.IsErrorWithCode(err, ldap.LDAPResultAttributeOrValueExists) {
			return fmt.Errorf("add %s to %s: %w", comp.Name, g, err)
		}
	}
	return nil
}

const passwordLength = 120

// password is a random password as unicodePwd takes it: in double quotes,
// UTF-16LE. It has upper and lower case letters, digits and symbols, as
// a complexity policy wants.
func password() (string, error) {
	const (
		upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		lower   = "abcdefghijkmnopqrstuvwxyz"
		digits  = "23456789"
		symbols = "!#%+-./:=?@_~"
	)
	sets := []string{upper, lower, digits, symbols, upper + lower + digits + symbols}
	b := make([]byte, passwordLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	pw := make([]rune, passwordLength)
	for i := range pw {
		set := sets[min(i, len(sets)-1)]
		pw[i] = rune(set[int(b[i])%len(set)])
	}
	encoded := utf16.Encode([]rune(`"` + string(pw) + `"`))
	out := make([]byte, 2*len(encoded))
	for i, u := range encoded {
		binary.LittleEndian.PutUint16(out[2*i:], u)
	}
	return string(out), nil
}

// Refused says the directory refused what was asked for good: the
// service account may not, or the object breaks a constraint.
func Refused(err error) bool {
	return ldap.IsErrorAnyOf(err, ldap.LDAPResultInsufficientAccessRights, ldap.LDAPResultUnwillingToPerform,
		ldap.LDAPResultConstraintViolation, ldap.LDAPResultObjectClassViolation, ldap.LDAPResultNoSuchObject)
}

// exists: Active Directory answers 68 for a DN taken and for a
// sAMAccountName another object holds.
func exists(err error) bool { return ldap.IsErrorWithCode(err, ldap.LDAPResultEntryAlreadyExists) }

// Delete deletes the object, and what Windows put under it (a cluster's
// or BitLocker's objects); one already gone is deleted.
func (c *Client) Delete(ctx context.Context, dn string) error {
	conn, done, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer done()
	err = conn.Del(ldap.NewDelRequest(dn, nil))
	if ldap.IsErrorWithCode(err, ldap.LDAPResultNotAllowedOnNonLeaf) {
		err = conn.Del(ldap.NewDelRequest(dn, []ldap.Control{ldap.NewControlSubtreeDelete()}))
	}
	if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		return fmt.Errorf("delete %s: %w", dn, err)
	}
	return nil
}

// Rename gives the object a new name: its CN, sAMAccountName and
// dNSHostName. It returns its new DN.
func (c *Client) Rename(ctx context.Context, dn, name, dnsHostName string) (string, error) {
	conn, done, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer done()
	parsed, err := ldap.ParseDN(dn)
	if err != nil || len(parsed.RDNs) < 2 {
		return "", fmt.Errorf("%q is not a computer object's DN", dn)
	}
	parent := ParentDN(dn)
	newDN := ComputerDN(name, parent)
	if !strings.EqualFold(dn, newDN) {
		if err := conn.ModifyDN(ldap.NewModifyDNRequest(dn, "CN="+ldap.EscapeDN(strings.ToUpper(name)), true, "")); err != nil &&
			!ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return "", fmt.Errorf("rename %s: %w", dn, err)
		}
	}
	mod := ldap.NewModifyRequest(newDN, nil)
	mod.Replace("sAMAccountName", []string{Account(name)})
	if dnsHostName != "" {
		mod.Replace("dNSHostName", []string{dnsHostName})
	}
	if err := conn.Modify(mod); err != nil {
		return "", fmt.Errorf("rename %s: %w", newDN, err)
	}
	return newDN, nil
}

// ParentDN is a DN without its first part, as it is written: go-ldap's
// String lower-cases the attribute types.
func ParentDN(dn string) string {
	for i := 0; i < len(dn); i++ {
		switch dn[i] {
		case '\\':
			i++
		case ',':
			return strings.TrimSpace(dn[i+1:])
		}
	}
	return ""
}

// Account is a computer's sAMAccountName.
func Account(name string) string { return strings.ToUpper(name) + "$" }

// ComputerDN is the DN of the computer's object in an OU.
func ComputerDN(name, ou string) string {
	return "CN=" + ldap.EscapeDN(strings.ToUpper(name)) + "," + ou
}

// DomainDN is the domain a DN is in, its DC parts: OU=Web,DC=corp,DC=example
// is in DC=corp,DC=example. A DN with none is itself.
func DomainDN(dn string) string {
	parsed, err := ldap.ParseDN(dn)
	if err != nil {
		return dn
	}
	for i, rdn := range parsed.RDNs {
		if len(rdn.Attributes) > 0 && strings.EqualFold(rdn.Attributes[0].Type, "dc") {
			return dnFrom(dn, i)
		}
	}
	return dn
}

// dnFrom is a DN without its first n parts, as it is written.
func dnFrom(dn string, n int) string {
	for range n {
		dn = ParentDN(dn)
	}
	return dn
}

// SameDN says whether two DNs name the same entry, whatever their case
// and spacing.
func SameDN(a, b string) bool {
	da, err := ldap.ParseDN(a)
	if err != nil {
		return strings.EqualFold(a, b)
	}
	db, err := ldap.ParseDN(b)
	if err != nil {
		return false
	}
	return da.EqualFold(db)
}

// Domain is a DN's DNS domain, its DC parts: DC=corp,DC=example is
// corp.example.
func Domain(dn string) string {
	parsed, err := ldap.ParseDN(dn)
	if err != nil {
		return ""
	}
	var parts []string
	for _, rdn := range parsed.RDNs {
		for _, a := range rdn.Attributes {
			if strings.EqualFold(a.Type, "dc") {
				parts = append(parts, strings.ToLower(a.Value))
			}
		}
	}
	return strings.Join(parts, ".")
}

// CheckDN says what is wrong with a DN that must be under the base DN
// ("" when it is right).
func CheckDN(dn, base string) string {
	d, err := ldap.ParseDN(dn)
	if err != nil || len(d.RDNs) == 0 {
		return fmt.Sprintf("%q is not a DN: for example OU=Servers,%s.", dn, cmpOr(base, "DC=corp,DC=example"))
	}
	if base == "" {
		return ""
	}
	b, err := ldap.ParseDN(base)
	if err == nil && !b.EqualFold(d) && !b.AncestorOfFold(d) {
		return fmt.Sprintf("%s is not under the directory's base DN, %s.", dn, base)
	}
	return ""
}

// Resolve is a DN given relative to the base DN, as vRA's relative DNs
// are, made whole: one with no DC part is under the base DN.
func Resolve(dn, base string) string {
	dn = strings.TrimSpace(dn)
	d, err := ldap.ParseDN(dn)
	if err != nil || base == "" || dn == "" {
		return dn
	}
	for _, r := range d.RDNs {
		for _, a := range r.Attributes {
			if strings.EqualFold(a.Type, "DC") {
				return dn
			}
		}
	}
	return dn + "," + base
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Inspect reads the certificate the directory presents: on connecting
// (LDAPS) or after StartTLS.
func Inspect(ctx context.Context, rawURL string, o trust.Options) (trust.Certificate, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return trust.Certificate{}, "", fmt.Errorf("%q is not an LDAP URL", rawURL)
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = map[string]string{"ldaps": "636"}[u.Scheme]
		if port == "" {
			port = "389"
		}
	}
	addr := net.JoinHostPort(host, port)
	if u.Scheme == "ldaps" {
		c, err := trust.Inspect(ctx, "ldaps://"+addr, o)
		return c, addr, err
	}
	conn, err := ldap.DialURL("ldap://"+addr, ldap.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}))
	if err != nil {
		return trust.Certificate{}, addr, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetTimeout(timeout)
	if err := conn.StartTLS(&tls.Config{ServerName: host, InsecureSkipVerify: true}); err != nil { //nolint:gosec // read, then checked
		return trust.Certificate{}, addr, fmt.Errorf("StartTLS: %w", err)
	}
	state, _ := conn.TLSConnectionState()
	c, err := trust.Describe(state.PeerCertificates, host, o)
	return c, addr, err
}
