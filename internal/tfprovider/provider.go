// Package tfprovider is rvav's Terraform provider (ADR-0053): projects,
// catalog items, policies, roles and their bindings, subscriptions and
// deployments as configuration, through pkg/rvavclient.
package tfprovider

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/oauth2"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// Address is the provider's source address, as configurations name it.
const Address = "registry.terraform.io/damhau/rvav"

// New returns the provider of this version.
func New(version string) func() provider.Provider {
	return NewPolling(version, 2*time.Second)
}

// NewPolling is New, reading a request it waits for every poll.
func NewPolling(version string, poll time.Duration) func() provider.Provider {
	return func() provider.Provider { return &rvavProvider{version: version, poll: poll} }
}

type rvavProvider struct {
	version string
	poll    time.Duration
	// getenv and configPath stand in for the process's in tests.
	getenv     func(string) string
	configPath string
}

type providerModel struct {
	URL          types.String `tfsdk:"url"`
	Token        types.String `tfsdk:"token"`
	Profile      types.String `tfsdk:"profile"`
	InsecureHTTP types.Bool   `tfsdk:"insecure_http"`
}

func (p *rvavProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "rvav"
	resp.Version = p.version
}

func (p *rvavProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages rvav's projects, catalog items, policies, roles, subscriptions and deployments.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "rvav's address, for example `https://rvav.corp.example`. Default: `RVAV_URL`, else the rvavctl profile's.",
			},
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "An API token (`rvav_pat_…`). Default: `RVAV_TOKEN`, else the rvavctl profile's sign-in.",
			},
			"profile": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The rvavctl profile to sign in with when no token is given (`rvavctl login`). Default: `default`.",
			},
			"insecure_http": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Sends the token to an `http://` address of another machine, unencrypted, as `rvavctl --insecure-http` does: " +
					"for a test rvav only. An `http://` address of this machine, such as `http://localhost:8080`, needs nothing. " +
					"Default: `false`, unless the rvavctl profile signed in with `rvavctl login --insecure-http`.",
			},
		},
	}
}

func (p *rvavProvider) env(k string) string {
	if p.getenv != nil {
		return p.getenv(k)
	}
	return os.Getenv(k)
}

func (p *rvavProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.URL.IsUnknown() || cfg.Token.IsUnknown() || cfg.Profile.IsUnknown() || cfg.InsecureHTTP.IsUnknown() {
		resp.Diagnostics.AddError("Unknown provider settings",
			"url, token, profile and insecure_http must be known when Terraform plans: give them as values or variables.")
		return
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	server, tokens, err := p.target(cfg, httpClient)
	if err != nil {
		resp.Diagnostics.AddError("Can't connect to rvav", err.Error())
		return
	}
	c, err := rvavclient.Connect(server, tokens, rvavclient.WithHTTPClient(httpClient),
		rvavclient.UserAgent("terraform-provider-rvav/"+p.version))
	if err != nil {
		resp.Diagnostics.AddError("Can't connect to rvav", err.Error())
		return
	}
	cn := &conn{api: c, server: server, poll: p.poll}
	resp.ResourceData, resp.DataSourceData = cn, cn
}

// target is the rvav to call and the token to call it with, as rvavctl
// chooses them: the token given or RVAV_TOKEN, else the profile's sign-in,
// which goes to the profile's rvav only. A token goes to an http:// address
// of another machine only when insecure_http, or the profile, says so.
func (p *rvavProvider) target(cfg providerModel, httpClient *http.Client) (string, oauth2.TokenSource, error) {
	server, tokens, insecure, err := p.pick(cfg, httpClient)
	if err != nil {
		return "", nil, err
	}
	if tokens != nil && !insecure && !cfg.InsecureHTTP.ValueBool() && rvavclient.Unencrypted(server) {
		return "", nil, fmt.Errorf("%s would get the token unencrypted: use its https:// address, or set insecure_http = true", server)
	}
	return server, tokens, nil
}

// pick is target's choice, and whether the profile chose plain http.
func (p *rvavProvider) pick(cfg providerModel, httpClient *http.Client) (string, oauth2.TokenSource, bool, error) {
	server := cfg.URL.ValueString()
	if server == "" {
		server = p.env("RVAV_URL")
	}
	token := cfg.Token.ValueString()
	if token == "" {
		token = p.env("RVAV_TOKEN")
	}
	if token != "" {
		if server == "" {
			return "", nil, false, errors.New("a token is given but no address: set url, or RVAV_URL")
		}
		return server, rvavclient.StaticToken(token), false, nil
	}
	path := p.configPath
	if path == "" {
		var err error
		if path, err = rvavclient.DefaultProfilesPath(); err != nil {
			return "", nil, false, err
		}
	}
	ps, err := rvavclient.LoadProfiles(path)
	if err != nil {
		return "", nil, false, err
	}
	name := cfg.Profile.ValueString()
	prof := ps.Profiles[cmp.Or(name, "default")]
	switch {
	case prof == nil && name != "":
		return "", nil, false, fmt.Errorf("no rvavctl profile %q in %s: sign in with rvavctl login --profile %s", name, path, name)
	case prof != nil && (server == "" || sameServer(server, prof.Server)):
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
		return prof.Server, ps.TokenSource(ctx, cmp.Or(name, "default")), prof.InsecureHTTP, nil
	case server == "":
		return "", nil, false, errors.New("no rvav to call: set url and token (or RVAV_URL and RVAV_TOKEN), or sign in with rvavctl login")
	}
	// An rvav in development mode signs everyone in as one user.
	return server, nil, false, nil
}

func sameServer(a, b string) bool {
	x, err1 := rvavclient.BaseURL(a)
	y, err2 := rvavclient.BaseURL(b)
	return err1 == nil && err2 == nil && strings.EqualFold(x, y)
}

func (p *rvavProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newProjectResource,
		newCatalogItemResource,
		newApprovalPolicyResource,
		newDay2PolicyResource,
		newNamingPolicyResource,
		newLeasePolicyResource,
		newLeaseRuleResource,
		newDeploymentLimitPolicyResource,
		newProjectQuotasResource,
		newGlobalQuotasResource,
		newRoleResource,
		newRoleAssignmentResource,
		newProjectMemberResource,
		newGroupMappingResource,
		newSubscriptionResource,
		newSecretResource,
		newIPRangeResource,
		newDeploymentResource,
	}
}

func (p *rvavProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newProjectDataSource,
		newCatalogItemDataSource,
		newUserDataSource,
	}
}
