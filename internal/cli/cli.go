// Package cli is rvavctl, rvav's command line for users (ADR-0051): sign
// in, the catalog, requests, deployments and their day-2 actions, and
// approvals, through the client generated from the API's contract.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/damhau/rvav/internal/buildinfo"
	"github.com/damhau/rvav/pkg/rvavclient"
)

// Env is what rvavctl reads and writes outside itself, so that tests can
// give their own.
type Env struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
	// Getenv reads the environment: RVAV_URL and RVAV_TOKEN.
	Getenv func(string) string
	// ConfigPath is the profiles file; rvavclient.DefaultProfilesPath when empty.
	ConfigPath string
	// OpenBrowser opens the identity provider's sign-in page.
	OpenBrowser func(url string) error
	// HTTP calls rvav and the identity provider.
	HTTP *http.Client
	// Poll is how often a wait reads the request again.
	Poll time.Duration
}

// OSEnv is the process's own environment.
func OSEnv() Env {
	return Env{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Getenv: os.Getenv, OpenBrowser: openBrowser,
		HTTP: &http.Client{Timeout: 2 * time.Minute}, Poll: 2 * time.Second}
}

// app is one run of rvavctl: its environment and global flags.
type app struct {
	Env
	profile string
	server  string
	output  string
}

// Main runs rvavctl with args (without the program's name) and returns its
// exit code.
func Main(ctx context.Context, args []string, env Env) int {
	a := &app{Env: env}
	root := a.root()
	root.SetArgs(args)
	root.SetIn(env.In)
	root.SetOut(env.Out)
	root.SetErr(env.Err)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(env.Err, "rvavctl: "+message(err))
		return 1
	}
	return 0
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "rvavctl",
		Short: "rvav from the command line: the catalog, requests, deployments and approvals",
		Long: `rvavctl requests from rvav's catalog, follows requests, runs day-2 actions on
deployments and decides approvals, as the user who signed in.

Sign in once with "rvavctl login --server https://rvav.corp.example". In
scripts, set RVAV_URL and RVAV_TOKEN (an API token) instead.`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		Version:           buildinfo.Version(),
		PersistentPreRunE: func(*cobra.Command, []string) error { return a.checkOutput() },
	}
	f := root.PersistentFlags()
	f.StringVar(&a.profile, "profile", "default", "the profile to use, one per rvav signed in to")
	f.StringVar(&a.server, "server", "", "rvav's address, for example https://rvav.corp.example (RVAV_URL)")
	f.StringVarP(&a.output, "output", "o", "table", "table, or json for what the API returns")
	root.AddCommand(a.loginCmd(), a.logoutCmd(), a.whoamiCmd(), a.catalogCmd(), a.requestsCmd(), a.deploymentsCmd(),
		a.approvalsCmd(), a.ipRangesCmd())
	return root
}

func (a *app) checkOutput() error {
	if a.output != "table" && a.output != "json" {
		return fmt.Errorf("--output %q: use table or json", a.output)
	}
	return nil
}

func (a *app) getenv(k string) string {
	if a.Getenv == nil {
		return ""
	}
	return a.Getenv(k)
}

func (a *app) configPath() (string, error) {
	if a.ConfigPath != "" {
		return a.ConfigPath, nil
	}
	return rvavclient.DefaultProfilesPath()
}

func (a *app) profiles() (*rvavclient.Profiles, error) {
	path, err := a.configPath()
	if err != nil {
		return nil, err
	}
	return rvavclient.LoadProfiles(path)
}

// target is the rvav to call and the token to call it with: RVAV_TOKEN
// first, then the profile's own, which goes to the profile's rvav only.
func (a *app) target(ctx context.Context) (string, oauth2.TokenSource, error) {
	server := a.server
	if server == "" {
		server = a.getenv("RVAV_URL")
	}
	ps, err := a.profiles()
	if err != nil {
		return "", nil, err
	}
	p := ps.Profiles[a.profile]
	if token := a.getenv("RVAV_TOKEN"); token != "" {
		if server == "" && p != nil {
			server = p.Server
		}
		if server == "" {
			return "", nil, errors.New("RVAV_TOKEN is set but not RVAV_URL: set it, or give --server")
		}
		return server, rvavclient.StaticToken(token), nil
	}
	if p != nil && (server == "" || sameServer(server, p.Server)) {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, a.HTTP)
		return p.Server, p.TokenSource(ctx, a.saveToken(ps.Path)), nil
	}
	if server == "" {
		return "", nil, fmt.Errorf("not signed in (profile %q): run rvavctl login --server https://rvav.corp.example", a.profile)
	}
	return server, nil, nil
}

func (a *app) saveToken(path string) func(*oauth2.Token) error {
	return func(t *oauth2.Token) error {
		return rvavclient.Update(path, a.profile, func(p *rvavclient.Profile) {
			if p.OIDC != nil {
				p.OIDC.Token = t
			}
		})
	}
}

func sameServer(a, b string) bool {
	x, err1 := rvavclient.BaseURL(a)
	y, err2 := rvavclient.BaseURL(b)
	return err1 == nil && err2 == nil && strings.EqualFold(x, y)
}

// connect is a client of the rvav the flags, the environment or the
// profile name.
func (a *app) connect(ctx context.Context) (*rvavclient.ClientWithResponses, error) {
	server, tokens, err := a.target(ctx)
	if err != nil {
		return nil, err
	}
	return a.client(server, tokens)
}

func (a *app) client(server string, tokens oauth2.TokenSource) (*rvavclient.ClientWithResponses, error) {
	return rvavclient.Connect(server, tokens, rvavclient.WithHTTPClient(a.HTTP),
		rvavclient.UserAgent("rvavctl/"+buildinfo.Version()))
}

// message is an error as rvavctl says it: what to do when the sign-in is
// the problem.
func message(err error) string {
	switch {
	case errors.Is(err, rvavclient.ErrSignInExpired):
		return err.Error() + "\nSign in again: rvavctl login"
	case rvavclient.IsStatus(err, http.StatusUnauthorized):
		return err.Error() + "\nrvav doesn't accept this sign-in: run rvavctl login, or check RVAV_TOKEN"
	}
	var e *rvavclient.Error
	if errors.As(err, &e) && e.Problem != nil && e.Problem.Errors != nil {
		for _, fe := range *e.Problem.Errors {
			if fe.Code != nil && *fe.Code == "warning" {
				return err.Error() + "\nTo go on despite these warnings, add --accept-warnings"
			}
		}
	}
	return err.Error()
}
