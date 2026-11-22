package ad_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/integrations/ad"
	"github.com/damhau/rvav/internal/integrations/ad/adtest"
	"github.com/damhau/rvav/internal/trust"
)

const servers = "OU=Servers," + adtest.BaseDN

func client(s *adtest.Server, url string, o trust.Options) *ad.Client {
	return ad.New(ad.Config{URL: url, Username: s.BindDN, Password: s.Password, BaseDN: adtest.BaseDN, TLS: o})
}

func TestConnections(t *testing.T) {
	s := adtest.Start(t, []string{servers})
	ctx := context.Background()
	pin := trust.Options{Pin: trust.Fingerprint(s.Cert)}
	for _, url := range []string{s.LDAPS, s.StartTLS} {
		if err := client(s, url, pin).Test(ctx); err != nil {
			t.Errorf("%s pinned: %v", url, err)
		}
		// Neither the CAs nor a pin vouch for it: refused, StartTLS too.
		if err := client(s, url, trust.Options{}).Test(ctx); err == nil {
			t.Errorf("%s: an unknown certificate was trusted", url)
		}
		c, _, err := ad.Inspect(ctx, url, trust.Options{})
		if err != nil || c.SHA256 != trust.Fingerprint(s.Cert) || c.Trusted {
			t.Errorf("%s inspected = %+v, %v", url, c, err)
		}
	}
	bad := ad.New(ad.Config{URL: s.LDAPS, Username: s.BindDN, Password: "not-the-password!", BaseDN: adtest.BaseDN, TLS: pin})
	if err := bad.Test(ctx); err == nil || !strings.Contains(err.Error(), "bind") {
		t.Errorf("a wrong password = %v", err)
	}
}

func TestComputerObjects(t *testing.T) {
	group := "CN=Linux servers,OU=Groups," + adtest.BaseDN
	s := adtest.Start(t, []string{servers}, group)
	ctx := context.Background()
	c := client(s, s.LDAPS, trust.Options{Pin: trust.Fingerprint(s.Cert)})
	comp := ad.Computer{Name: "web-dev-app001", DN: ad.ComputerDN("web-dev-app001", servers), DNSHostName: "web-dev-app001.corp.example",
		Description: "rvav: request req_1, deployment dep_1"}
	if err := c.Create(ctx, comp, []string{group}); err != nil {
		t.Fatal(err)
	}
	e := s.Entry(comp.DN)
	if e["sAMAccountName"][0] != "WEB-DEV-APP001$" || e["userAccountControl"][0] != "4096" ||
		e["dNSHostName"][0] != comp.DNSHostName || e["description"][0] != comp.Description || !slices.Contains(e["objectClass"], "computer") {
		t.Fatalf("computer object = %v", e)
	}
	if !slices.Contains(s.Entry(group)["member"], comp.DN) {
		t.Fatalf("group = %v", s.Entry(group))
	}
	// A password no one is told, of the length and kinds of characters a
	// complexity policy wants.
	pw := s.ComputerPassword(comp.DN)
	if len(pw) != 120 || !strings.ContainsAny(pw, "ABCDEFGHJKLMNPQRSTUVWXYZ") || !strings.ContainsAny(pw, "abcdefghijkmnopqrstuvwxyz") ||
		!strings.ContainsAny(pw, "23456789") || !strings.ContainsAny(pw, "!#%+-./:=?@_~") {
		t.Fatalf("password = %q", pw)
	}
	// Made again by a retry: the same object, made once.
	if err := c.Create(ctx, comp, []string{group}); err != nil || s.Adds() != 1 {
		t.Fatalf("again = %v, %d adds", err, s.Adds())
	}
	found, err := c.Find(ctx, "WEB-DEV-APP001")
	if err != nil || found == nil || !strings.EqualFold(found.DN, comp.DN) {
		t.Fatalf("found = %+v, %v", found, err)
	}
	// Someone else's of that name, in another OU: refused.
	other := comp
	other.DN, other.Description = ad.ComputerDN("web-dev-app001", "OU=Other,"+adtest.BaseDN), ""
	s.Put("OU=Other,"+adtest.BaseDN, map[string][]string{"objectClass": {"organizationalUnit"}})
	if err := c.Create(ctx, other, nil); !errors.Is(err, ad.ErrExists) {
		t.Fatalf("a name taken = %v", err)
	}
	if err := c.Delete(ctx, comp.DN); err != nil || s.Entry(comp.DN) != nil || len(s.Entry(group)["member"]) != 0 {
		t.Fatalf("delete = %v, %v", err, s.Entry(comp.DN))
	}
	if err := c.Delete(ctx, comp.DN); err != nil {
		t.Fatalf("deleted again = %v", err)
	}
	if found, err := c.Find(ctx, "web-dev-app001"); err != nil || found != nil {
		t.Fatalf("found after delete = %+v, %v", found, err)
	}
	s.FailDelete(true)
	if err := c.Create(ctx, comp, nil); err != nil {
		t.Fatal(err)
	}
	if again := s.ComputerPassword(comp.DN); again == "" || again == pw {
		t.Fatalf("made again with password %q, first %q", again, pw)
	}
	if err := c.Delete(ctx, comp.DN); err == nil {
		t.Fatal("a delete refused succeeded")
	}
}

// TestComputerNames: a name is looked for in the whole domain, though
// the base DN is an OU in it; over plain LDAP, on which Active Directory
// sets no password, the object is made with none.
func TestComputerNames(t *testing.T) {
	web, other := "OU=Web,"+adtest.BaseDN, "OU=Other,"+adtest.BaseDN
	s := adtest.Start(t, []string{web, other})
	ctx := context.Background()
	s.Put("CN=WEB-DEV-APP01,"+other, map[string][]string{"objectClass": {"computer"}, "sAMAccountName": {"WEB-DEV-APP01$"}})
	c := ad.New(ad.Config{URL: s.LDAPS, Username: s.BindDN, Password: s.Password, BaseDN: web,
		TLS: trust.Options{Pin: trust.Fingerprint(s.Cert)}})
	if found, err := c.Find(ctx, "web-dev-app01"); err != nil || found == nil || !ad.SameDN(found.DN, "cn=web-dev-app01, ou=other,"+adtest.BaseDN) {
		t.Fatalf("a name outside the base DN = %+v, %v", found, err)
	}
	plain := ad.New(ad.Config{URL: s.StartTLS, Username: s.BindDN, Password: s.Password, BaseDN: adtest.BaseDN, PlainLDAP: true})
	comp := ad.Computer{Name: "web-dev-app02", DN: ad.ComputerDN("web-dev-app02", web)}
	if err := plain.Create(ctx, comp, nil); err != nil || s.Entry(comp.DN) == nil || s.ComputerPassword(comp.DN) != "" {
		t.Fatalf("over plain LDAP = %v, password %q", err, s.ComputerPassword(comp.DN))
	}
}

func TestNames(t *testing.T) {
	for dn, want := range map[string]string{
		"OU=Web,DC=corp,DC=example":    "DC=corp,DC=example",
		`CN=x\,y,OU=Web,DC=Corp,DC=ex`: "DC=Corp,DC=ex",
		"DC=corp,DC=example":           "DC=corp,DC=example",
		"OU=Web":                       "OU=Web",
	} {
		if got := ad.DomainDN(dn); got != want {
			t.Errorf("DomainDN(%q) = %q, want %q", dn, got, want)
		}
	}
	if !ad.SameDN("CN=A,OU=Web,DC=corp,DC=example", "cn=a, ou=web,dc=CORP,dc=example") || ad.SameDN("CN=A,OU=Web", "CN=A,OU=Other") {
		t.Error("SameDN")
	}
	if d := ad.Domain("OU=Servers,DC=Corp,DC=Example"); d != "corp.example" {
		t.Errorf("domain = %q", d)
	}
	for dn, want := range map[string]string{
		"OU=Servers,DC=corp,DC=example":     "",
		"ou=servers,dc=CORP,dc=example":     "",
		"OU=Servers,DC=other,DC=example":    "not under",
		"Servers":                           "not a DN",
		"CN=x,OU=a=b,DC=corp,DC=example,,,": "not a DN",
	} {
		if got := ad.CheckDN(dn, "DC=corp,DC=example"); want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("CheckDN(%q) = %q, want %q", dn, got, want)
		}
	}
	for dn, want := range map[string]string{
		"OU=Servers,OU=Web":             "OU=Servers,OU=Web,DC=corp,DC=example",
		" OU=Servers ":                  "OU=Servers,DC=corp,DC=example",
		"OU=Servers,dc=other,dc=com":    "OU=Servers,dc=other,dc=com",
		"OU=Servers,DC=corp,DC=example": "OU=Servers,DC=corp,DC=example",
		"Servers":                       "Servers",
	} {
		if got := ad.Resolve(dn, "DC=corp,DC=example"); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", dn, got, want)
		}
	}
	if got := ad.Resolve("OU=Servers", ""); got != "OU=Servers" {
		t.Errorf("Resolve without a base DN = %q", got)
	}
}
