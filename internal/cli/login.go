package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/damhau/rvav/pkg/rvavclient"
)

type loginFlags struct {
	withToken    bool
	device       bool
	clientID     string
	callbackPort int
}

func (a *app) loginCmd() *cobra.Command {
	var f loginFlags
	c := &cobra.Command{
		Use:   "login",
		Short: "Sign in to an rvav and keep the sign-in in a profile",
		Long: `Sign in to the rvav at --server, as rvav's web app does: in the browser at
its identity provider, or with --device on a machine without one (open the
address shown on any device and enter the code). The sign-in is kept in your
configuration directory and refreshed as needed.

With --with-token, rvavctl reads an API token on standard input instead (your
menu in rvav, API tokens). A token acts with the roles rvav gives you, not
those from your groups.`,
		Example: `  rvavctl login --server https://rvav.corp.example
  rvavctl login --server https://rvav.corp.example --device
  rvavctl login --server https://rvav.corp.example --with-token < token.txt
  rvavctl login --server https://rvav-test.corp.example --profile test`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.login(cmd.Context(), f) },
	}
	c.Flags().BoolVar(&f.withToken, "with-token", false, "read an API token on standard input")
	c.Flags().BoolVar(&f.device, "device", false, "sign in on another device, with a code")
	c.Flags().StringVar(&f.clientID, "client-id", "", "the identity provider's client for rvavctl, when it isn't the web app's")
	c.Flags().IntVar(&f.callbackPort, "callback-port", 0, "the local port the browser comes back to (any free one by default)")
	return c
}

func (a *app) login(ctx context.Context, f loginFlags) error {
	ps, err := a.profiles()
	if err != nil {
		return err
	}
	server := a.server
	if server == "" {
		server = a.getenv("RVAV_URL")
	}
	if server == "" && ps.Profiles[a.profile] != nil {
		server = ps.Profiles[a.profile].Server
	}
	if server == "" {
		return usagef("give rvav's address: rvavctl login --server https://rvav.corp.example")
	}
	if _, err := rvavclient.BaseURL(server); err != nil {
		return &usageError{err}
	}
	if err := a.encrypted(server); err != nil {
		return err
	}
	server = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(server), "/"), rvavclient.APIPath)
	p := &rvavclient.Profile{Server: server, InsecureHTTP: rvavclient.Unencrypted(server)}
	how := ""
	switch {
	case f.withToken:
		b, err := io.ReadAll(io.LimitReader(a.In, 4096))
		if err != nil {
			return err
		}
		if p.Token = strings.TrimSpace(string(b)); p.Token == "" {
			return usagef("no token on standard input")
		}
		how = ", with an API token"
	default:
		anon, err := a.client(server, nil)
		if err != nil {
			return err
		}
		opts, err := anon.GetAuthOptionsWithResponse(ctx)
		if err != nil {
			return err
		}
		if err := rvavclient.Check(opts.HTTPResponse, opts.Body); err != nil {
			return err
		}
		if opts.JSON200.Provider == nil {
			how, err = a.noProvider(ctx, anon, server)
			if err != nil {
				return err
			}
			break
		}
		if p.OIDC, err = a.signIn(ctx, *opts.JSON200.Provider, f); err != nil {
			var se *signInError
			if errors.As(err, &se) {
				a.report(ctx, anon, rvavclient.SignInReport{Client: rvavclient.SignInClientRvavctl, Method: method(f),
					Failure: se.failure()})
			}
			return err
		}
	}
	// A provider may rotate refresh tokens: keep the latest.
	keep := func(t *oauth2.Token) error { p.OIDC.Token = t; return nil }
	c, err := a.client(server, p.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, a.HTTP), keep))
	if err != nil {
		return err
	}
	a.report(ctx, c, rvavclient.SignInReport{Client: rvavclient.SignInClientRvavctl, Method: method(f)})
	me, err := c.GetMeWithResponse(ctx)
	if err != nil {
		return err
	}
	if err := rvavclient.Check(me.HTTPResponse, me.Body); err != nil {
		return fmt.Errorf("rvav refused the sign-in: %w", err)
	}
	if err := rvavclient.Edit(ps.Path, func(ps *rvavclient.Profiles) error {
		ps.Profiles[a.profile] = p
		return nil
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.Out, "Signed in to %s as %s (%s)%s.\n", server, me.JSON200.User.Name, me.JSON200.User.Id, how)
	return err
}

// method is how rvavctl signs in, as the audit log says it: rvav takes an
// API token's or the development sign-in's from the token itself.
func method(f loginFlags) rvavclient.SignInReportMethod {
	switch {
	case f.withToken:
		return rvavclient.SignInMethodToken
	case f.device:
		return rvavclient.SignInMethodDevice
	}
	return rvavclient.SignInMethodOidc
}

// report tells rvav how the sign-in went, for its audit log (ADR-0058). An
// rvav too old to record it, or one that doesn't answer, doesn't stop it.
func (a *app) report(ctx context.Context, c *rvavclient.ClientWithResponses, r rvavclient.SignInReport) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = c.ReportSignInWithResponse(ctx, r)
}

// noProvider is an rvav with no identity provider: in development mode it
// signs everyone in as one user; else only API tokens work.
func (a *app) noProvider(ctx context.Context, anon *rvavclient.ClientWithResponses, server string) (string, error) {
	me, err := anon.GetMeWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if me.StatusCode() == http.StatusUnauthorized {
		return "", fmt.Errorf(`%s has no identity provider: sign in with an API token instead.
Make one in rvav (your menu, API tokens), then run:
  rvavctl login --server %s --with-token < token.txt`, server, server)
	}
	if err := rvavclient.Check(me.HTTPResponse, me.Body); err != nil {
		return "", err
	}
	return ", the user its development sign-in gives everyone", nil
}

func (a *app) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out: revoke the profile's sign-in and forget it",
		Long: `Sign out of the profile's rvav. rvavctl revokes the sign-in at the identity
provider when the provider offers revocation (RFC 7009), then forgets it on
this machine; without revocation, the sign-in stays valid at the provider
until it expires. An API token stays valid until it expires or you revoke it
in rvav. rvav's audit log records the sign-out.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ps, err := a.profiles()
			if err != nil {
				return err
			}
			p := ps.Profiles[a.profile]
			if p == nil {
				_, err := fmt.Fprintf(a.Out, "Not signed in (profile %q).\n", a.profile)
				return err
			}
			a.signOut(cmd.Context(), ps)
			// Signing out may have refreshed the sign-in, and spent the
			// refresh token held here.
			if now, err := a.profiles(); err == nil && now.Profiles[a.profile] != nil {
				p = now.Profiles[a.profile]
			}
			revoked := a.revoke(cmd.Context(), p)
			if err := rvavclient.Edit(ps.Path, func(ps *rvavclient.Profiles) error {
				delete(ps.Profiles, a.profile)
				return nil
			}); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.Out, "Signed out of %s%s.\n", p.Server, revoked)
			return err
		},
	}
}

// signOut tells rvav, for its audit log; signing out goes on without it.
func (a *app) signOut(ctx context.Context, ps *rvavclient.Profiles) {
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, oauth2.HTTPClient, a.HTTP), 5*time.Second)
	defer cancel()
	c, err := a.client(ps.Profiles[a.profile].Server, ps.TokenSource(ctx, a.profile))
	if err == nil {
		_, _ = c.SignOutWithResponse(ctx)
	}
}

// revoke revokes the profile's refresh token at the identity provider,
// which ends the sign-in there, and says how it went.
func (a *app) revoke(ctx context.Context, p *rvavclient.Profile) string {
	switch {
	case p.Token != "":
		return "; the API token stays valid until it expires or you revoke it in rvav"
	case p.OIDC == nil || p.OIDC.Token == nil:
		return ""
	}
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, oauth2.HTTPClient, a.HTTP), 10*time.Second)
	defer cancel()
	err := revokeToken(ctx, a.HTTP, p.OIDC)
	switch {
	case errors.Is(err, errNoRevocation):
		return "; the identity provider offers no revocation, so the sign-in stays valid there until it expires"
	case err != nil:
		return "; the identity provider didn't revoke the sign-in (" + oneLine(err.Error(), 200) +
			"), so it stays valid there until it expires"
	}
	return ", and the identity provider revoked the sign-in"
}

func (a *app) whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Who rvav takes you for, and your roles in each project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.connect(cmd.Context())
			if err != nil {
				return err
			}
			r, err := c.GetMeWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
				return err
			}
			if a.jsonOut() {
				return a.printJSON(rawJSON(r.Body))
			}
			me := r.JSON200
			email := ""
			if me.User.Email != nil {
				email = string(*me.User.Email)
			}
			if err := details(a.Out, "Name", me.User.Name, "ID", me.User.Id, "Email", email,
				"Roles", roleNames(me.Roles)); err != nil {
				return err
			}
			if len(me.Projects) == 0 {
				return nil
			}
			fmt.Fprintln(a.Out)
			t := newTable(a.Out, "PROJECT", "ID", "ROLES")
			for _, p := range me.Projects {
				t.row(p.Project.Name, p.Project.Id, roleNames(p.Roles))
			}
			return t.flush()
		},
	}
}

func roleNames(rs []rvavclient.RoleRef) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}
