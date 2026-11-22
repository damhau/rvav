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
	"net/url"
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
	profile      string
	server       string
	output       string
	insecureHTTP bool
}

// rvavctl's exit codes, for scripts (docs/guides/cli.md).
const (
	// exitFailed is a request that didn't succeed, a dry run rvav would
	// refuse, a call rvav refused (not found, forbidden, a conflict), or
	// any error without a code of its own.
	exitFailed = 1
	// exitUsage is a command, a flag or an input that isn't right.
	exitUsage = 2
	// exitTimeout is a wait that --timeout ended.
	exitTimeout = 3
	// exitRvav is rvav, or its identity provider, out of reach or answering
	// 5xx, or no sign-in it takes: not signed in, expired, refused.
	exitRvav = 4
	// exitInterrupted is Ctrl-C, as a shell reports a process it ends.
	exitInterrupted = 130
)

// Main runs rvavctl with args (without the program's name) and returns its
// exit code.
func Main(ctx context.Context, args []string, env Env) int {
	a := &app{Env: env}
	root := a.root()
	ran := false
	markRun(root, &ran)
	root.SetArgs(args)
	root.SetIn(env.In)
	root.SetOut(env.Out)
	root.SetErr(env.Err)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(env.Err, "rvavctl: "+multiline(message(err)))
		return exitCode(err, ran)
	}
	return 0
}

// markRun has each command note that it started: cobra's own errors, about
// commands, flags and arguments, come before.
func markRun(c *cobra.Command, ran *bool) {
	if run := c.RunE; run != nil {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			*ran = true
			return run(cmd, args)
		}
	}
	for _, sub := range c.Commands() {
		markRun(sub, ran)
	}
}

func exitCode(err error, ran bool) int {
	var u *usageError
	var t *timeoutError
	var o *outcomeError
	switch {
	case !ran || errors.As(err, &u):
		return exitUsage
	case errors.Is(err, context.Canceled):
		return exitInterrupted
	case errors.As(err, &t):
		return exitTimeout
	case errors.As(err, &o):
		return exitFailed
	case callingRvav(err):
		return exitRvav
	}
	return exitFailed
}

func callingRvav(err error) bool {
	var e *rvavclient.Error
	if errors.As(err, &e) {
		return e.Status == http.StatusUnauthorized || rvavclient.Transient(err)
	}
	var ue *url.Error
	var se *signInError
	var re *oauth2.RetrieveError
	return errors.As(err, &ue) || errors.As(err, &se) || errors.As(err, &re) ||
		errors.Is(err, rvavclient.ErrSignInExpired) || errors.Is(err, errNotSignedIn) || rvavclient.Transient(err)
}

// usageError is a command used wrongly, before rvav was asked anything.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error {
	return &usageError{fmt.Errorf(format, args...)}
}

// timeoutError is a wait that --timeout ended; rvav goes on with the request.
type timeoutError struct{ what string }

func (e *timeoutError) Error() string { return e.what }

// outcomeError is a request that ended otherwise than asked.
type outcomeError struct{ what string }

func (e *outcomeError) Error() string { return e.what }

var errNotSignedIn = errors.New("not signed in")

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
	f.BoolVar(&a.insecureHTTP, "insecure-http", false, "send tokens to an http:// rvav on another machine, unencrypted")
	root.AddCommand(a.loginCmd(), a.logoutCmd(), a.whoamiCmd(), a.catalogCmd(), a.requestsCmd(), a.deploymentsCmd(),
		a.approvalsCmd(), a.ipRangesCmd())
	return root
}

// group is a command of commands: its help, and a usage error for a
// command it doesn't have, which cobra checks at the root only.
func group(use, short string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
		}
		return cmd.Help()
	}}
}

func (a *app) checkOutput() error {
	if a.output != "table" && a.output != "json" {
		return usagef("--output %q: use table or json", a.output)
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
	server, tokens, insecure, err := a.pick(ctx)
	if err == nil && tokens != nil && !insecure {
		err = a.encrypted(server)
	}
	if err != nil {
		return "", nil, err
	}
	return server, tokens, nil
}

// encrypted refuses to send a token to server unencrypted, unless
// --insecure-http says to.
func (a *app) encrypted(server string) error {
	if a.insecureHTTP || !rvavclient.Unencrypted(server) {
		return nil
	}
	return usagef("%s would get your token unencrypted: use its https:// address, or add --insecure-http", server)
}

// pick is target's choice, and whether the profile chose plain http.
func (a *app) pick(ctx context.Context) (string, oauth2.TokenSource, bool, error) {
	server := a.server
	if server == "" {
		server = a.getenv("RVAV_URL")
	}
	ps, err := a.profiles()
	if err != nil {
		return "", nil, false, err
	}
	p := ps.Profiles[a.profile]
	if token := a.getenv("RVAV_TOKEN"); token != "" {
		if server == "" && p != nil {
			server = p.Server
		}
		if server == "" {
			return "", nil, false, usagef("RVAV_TOKEN is set but not RVAV_URL: set it, or give --server")
		}
		return server, rvavclient.StaticToken(token), p != nil && p.InsecureHTTP && sameServer(server, p.Server), nil
	}
	if p != nil && (server == "" || sameServer(server, p.Server)) {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, a.HTTP)
		return p.Server, ps.TokenSource(ctx, a.profile), p.InsecureHTTP, nil
	}
	if server == "" {
		return "", nil, false, fmt.Errorf("%w (profile %q): run rvavctl login --server https://rvav.corp.example", errNotSignedIn, a.profile)
	}
	return server, nil, false, nil
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
	c, err := rvavclient.Connect(server, tokens, rvavclient.WithHTTPClient(a.HTTP),
		rvavclient.UserAgent("rvavctl/"+buildinfo.Version()))
	if err != nil {
		return nil, &usageError{err}
	}
	return c, nil
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
