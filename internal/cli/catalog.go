package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// project is the project a command acts in: the one given, by ID, code or
// name, else the caller's only one where they may request.
func (a *app) project(ctx context.Context, c *rvavclient.ClientWithResponses, given string) (string, error) {
	r, err := c.GetMeWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
		return "", err
	}
	if given != "" {
		for _, p := range r.JSON200.Projects {
			if p.Project.Id == given || strings.EqualFold(p.Project.Code, given) || strings.EqualFold(p.Project.Name, given) {
				return p.Project.Id, nil
			}
		}
		return given, nil
	}
	var mine []rvavclient.ProjectRef
	for _, p := range r.JSON200.Projects {
		if slices.Contains(p.Permissions, "request.submit") {
			mine = append(mine, p.Project)
		}
	}
	if len(mine) == 1 {
		return mine[0].Id, nil
	}
	if len(mine) == 0 {
		return "", fmt.Errorf("you may request in no project")
	}
	names := make([]string, len(mine))
	for i, p := range mine {
		names[i] = fmt.Sprintf("%s (%s)", p.Code, p.Name)
	}
	return "", fmt.Errorf("give --project: %s", strings.Join(names, ", "))
}

func (a *app) catalogCmd() *cobra.Command {
	c := &cobra.Command{Use: "catalog", Short: "The items you can request, and requesting them"}
	c.AddCommand(a.catalogListCmd(), a.catalogShowCmd(), a.catalogChoicesCmd(), a.catalogRequestCmd())
	return c
}

func (a *app) catalogListCmd() *cobra.Command {
	var project, search, category string
	var limit int
	c := &cobra.Command{
		Use:   "list",
		Short: "The catalog items you can request in a project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			pid, err := a.project(ctx, c, project)
			if err != nil {
				return err
			}
			items, raw, err := collect(limit, func(cursor *string, n int) (page[rvavclient.CatalogItem], error) {
				params := &rvavclient.ListCatalogItemsParams{ProjectId: pid, Limit: &n, Cursor: cursor}
				if search != "" {
					params.Q = &search
				}
				if category != "" {
					params.Category = &category
				}
				r, err := c.ListCatalogItemsWithResponse(ctx, params)
				if err != nil {
					return page[rvavclient.CatalogItem]{}, err
				}
				if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
					return page[rvavclient.CatalogItem]{}, err
				}
				return page[rvavclient.CatalogItem]{items: r.JSON200.Items, body: r.Body, next: r.JSON200.NextCursor}, nil
			})
			if err != nil {
				return err
			}
			return a.list(raw, "No catalog items you can request here.", func() error {
				t := newTable(a.Out, "ID", "NAME", "TYPE", "CATEGORY", "VERSION", "APPROVAL")
				for _, i := range items {
					t.row(i.Slug, i.Name, string(i.Type), i.Category, i.Version.Number, string(i.Approval))
				}
				return t.flush()
			})
		},
	}
	c.Flags().StringVarP(&project, "project", "p", "", "the project, by ID, code or name")
	c.Flags().StringVarP(&search, "search", "q", "", "items whose name or description has this text")
	c.Flags().StringVar(&category, "category", "", "items in this category, by key")
	c.Flags().IntVar(&limit, "limit", 200, "at most this many items")
	return c
}

// itemForm is an item and the form of the version requested.
func (a *app) itemForm(ctx context.Context, c *rvavclient.ClientWithResponses, pid, item, version string) (
	*rvavclient.GetCatalogItemResponse, *rvavclient.GetRequestFormResponse, error) {
	ir, err := c.GetCatalogItemWithResponse(ctx, item, &rvavclient.GetCatalogItemParams{ProjectId: pid})
	if err != nil {
		return nil, nil, err
	}
	if err := rvavclient.Check(ir.HTTPResponse, ir.Body); err != nil {
		return nil, nil, err
	}
	params := &rvavclient.GetRequestFormParams{ProjectId: pid}
	if version != "" {
		vs := []rvavclient.CatalogItemVersionRef{ir.JSON200.Version}
		if ir.JSON200.Versions != nil {
			vs = *ir.JSON200.Versions
		}
		i := slices.IndexFunc(vs, func(v rvavclient.CatalogItemVersionRef) bool { return v.Number == version || v.Id == version })
		if i < 0 {
			numbers := make([]string, len(vs))
			for i, v := range vs {
				numbers[i] = v.Number
			}
			return nil, nil, fmt.Errorf("%s has no requestable version %s: %s", item, version, strings.Join(numbers, ", "))
		}
		params.VersionId = &vs[i].Id
	}
	fr, err := c.GetRequestFormWithResponse(ctx, ir.JSON200.Id, params)
	if err != nil {
		return nil, nil, err
	}
	if err := rvavclient.Check(fr.HTTPResponse, fr.Body); err != nil {
		return nil, nil, err
	}
	return ir, fr, nil
}

func (a *app) catalogShowCmd() *cobra.Command {
	var project, version string
	c := &cobra.Command{
		Use:   "show ITEM",
		Short: "A catalog item and the inputs its request form takes",
		Long: `A catalog item and the inputs its request form takes: each one's type, whether
it's required, its default and its choices. With -o json, the item and its
form as the API returns them ({"item": ..., "form": ...}).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			pid, err := a.project(ctx, c, project)
			if err != nil {
				return err
			}
			ir, fr, err := a.itemForm(ctx, c, pid, args[0], version)
			if err != nil {
				return err
			}
			if a.jsonOut() {
				return a.printJSON(map[string]json.RawMessage{"item": ir.Body, "form": fr.Body})
			}
			i, f := ir.JSON200, fr.JSON200
			versions := ""
			if i.Versions != nil {
				var ns []string
				for _, v := range *i.Versions {
					ns = append(ns, v.Number)
				}
				versions = strings.Join(ns, ", ")
			}
			if err := details(a.Out, "ID", i.Slug, "Name", i.Name, "Summary", i.Summary, "Type", string(i.Type),
				"Category", i.Category, "Version", i.Version.Number, "Versions", versions,
				"Approval", string(i.Approval)); err != nil {
				return err
			}
			listed, err := a.printInputs(f.Schema, f.UiSchema, f.InitialValues, "(catalog choices)")
			if err == nil && listed {
				_, err = fmt.Fprintf(a.Out, "\nrvav lists some inputs' choices by environment and other inputs: rvavctl catalog choices %s INPUT --env ENV\n", args[0])
			}
			return err
		},
	}
	c.Flags().StringVarP(&project, "project", "p", "", "the project, by ID, code or name")
	c.Flags().StringVar(&version, "version", "", "a version to request, by number, when the item offers several")
	return c
}

func (a *app) catalogChoicesCmd() *cobra.Command {
	var project, env, version string
	var inputs inputFlags
	c := &cobra.Command{
		Use:   "choices ITEM INPUT",
		Short: "The choices rvav lists for an input, such as clusters or networks",
		Long: `The choices rvav lists for an input of an item's request form, or the value it
works out for it. Some depend on the environment and on other inputs: give
them as you would to rvavctl catalog request.`,
		Example: `  rvavctl catalog choices rhel9-server cluster --env dev
  rvavctl catalog choices rhel9-server datastore --env dev --input cluster=GVA-DEV-CL01`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			pid, err := a.project(ctx, c, project)
			if err != nil {
				return err
			}
			_, fr, err := a.itemForm(ctx, c, pid, args[0], version)
			if err != nil {
				return err
			}
			given, err := inputs.read(a.In, fr.JSON200.Schema)
			if err != nil {
				return err
			}
			r, err := c.ResolveFieldValuesWithResponse(ctx, rvavclient.FieldValuesQuery{VersionId: fr.JSON200.VersionId,
				ProjectId: pid, Environment: optional(env), Field: args[1], Inputs: withInitial(fr.JSON200.InitialValues, given)})
			if err != nil {
				return err
			}
			if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
				return err
			}
			if a.jsonOut() {
				return a.printJSON(rawJSON(r.Body))
			}
			v := r.JSON200
			if v.Options == nil {
				_, err := fmt.Fprintln(a.Out, shown(v.Value))
				return err
			}
			t := newTable(a.Out, "VALUE", "LABEL", "NOTE")
			for _, o := range *v.Options {
				note := str(o.Description)
				if o.Disabled != nil && *o.Disabled {
					note = "unavailable: " + str(o.DisabledReason)
				}
				t.row(o.Value, o.Label, note)
			}
			return t.flush()
		},
	}
	c.Flags().StringVarP(&project, "project", "p", "", "the project, by ID, code or name")
	c.Flags().StringVarP(&env, "env", "e", "", "the environment, by key, for example dev")
	c.Flags().StringVar(&version, "version", "", "a version, by number, when the item offers several")
	c.Flags().StringArrayVarP(&inputs.values, "input", "i", nil, "another input it depends on, name=value (repeat it)")
	c.Flags().StringVar(&inputs.file, "inputs", "", "a JSON or YAML file of inputs, - for standard input")
	return c
}

type requestFlags struct {
	project, env, version, name, description, justification, onBehalfOf, at string
	count, leaseDays                                                        int
	acceptWarnings, dryRun, wait                                            bool
	inputs                                                                  inputFlags
}

func (a *app) catalogRequestCmd() *cobra.Command {
	var f requestFlags
	c := &cobra.Command{
		Use:   "request ITEM",
		Short: "Request a catalog item",
		Long: `Request a catalog item in a project and environment. Give the form's inputs
with --input name=value (a list as a,b,c) or a JSON or YAML file with
--inputs; inputs left out take their defaults, as in the web app. See an
item's inputs with rvavctl catalog show ITEM.

With --dry-run, rvav checks the request and says what it would do, without
making it. With --wait, rvavctl waits for the request to finish, and fails
when it doesn't succeed.`,
		Example: `  rvavctl catalog request rhel9-server --project web --env dev \
    --input role=app --input size=M --input post_config=linux-baseline,monitoring-agent --wait
  rvavctl catalog request rhel9-server --env prd --inputs server.yaml --justification "CHG-1234" --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return a.request(cmd.Context(), args[0], f) },
	}
	fl := c.Flags()
	fl.StringVarP(&f.project, "project", "p", "", "the project, by ID, code or name")
	fl.StringVarP(&f.env, "env", "e", "", "the environment, by key, for example dev")
	fl.StringVar(&f.version, "version", "", "a version to request, by number, when the item offers several")
	fl.StringArrayVarP(&f.inputs.values, "input", "i", nil, "an input, name=value (repeat it)")
	fl.StringVar(&f.inputs.file, "inputs", "", "a JSON or YAML file of inputs, - for standard input")
	fl.IntVar(&f.count, "count", 0, "how many identical deployments to make (1 by default)")
	fl.StringVar(&f.name, "name", "", "the deployment's name, instead of the one rvav gives")
	fl.StringVar(&f.description, "description", "", "what the deployment is for")
	fl.StringVar(&f.justification, "justification", "", "why, for the approvers")
	fl.IntVar(&f.leaseDays, "lease-days", 0, "a lease other than the policy's default, in days")
	fl.StringVar(&f.onBehalfOf, "on-behalf-of", "", "the future owner's user ID, for project admins")
	fl.StringVar(&f.at, "at", "", "start later, at this time (RFC 3339, for example 2026-12-01T06:00:00Z)")
	fl.BoolVar(&f.acceptWarnings, "accept-warnings", false, "go on despite soft policies' warnings")
	fl.BoolVar(&f.dryRun, "dry-run", false, "check the request and say what it would do, without making it")
	fl.BoolVarP(&f.wait, "wait", "w", false, "wait for the request to finish")
	_ = c.MarkFlagRequired("env")
	return c
}

func (a *app) request(ctx context.Context, item string, f requestFlags) error {
	c, err := a.connect(ctx)
	if err != nil {
		return err
	}
	pid, err := a.project(ctx, c, f.project)
	if err != nil {
		return err
	}
	ir, fr, err := a.itemForm(ctx, c, pid, item, f.version)
	if err != nil {
		return err
	}
	given, err := f.inputs.read(a.In, fr.JSON200.Schema)
	if err != nil {
		return err
	}
	d := rvavclient.RequestDraft{CatalogItemId: ir.JSON200.Id, VersionId: fr.JSON200.VersionId, ProjectId: pid,
		Environment: f.env, Inputs: withInitial(fr.JSON200.InitialValues, given)}
	if f.count > 0 {
		d.Count = &f.count
	}
	if f.leaseDays > 0 {
		d.LeaseDays = &f.leaseDays
	}
	d.DeploymentName, d.DeploymentDescription = optional(f.name), optional(f.description)
	d.Justification, d.OnBehalfOf = optional(f.justification), optional(f.onBehalfOf)
	if f.at != "" {
		at, err := time.Parse(time.RFC3339, f.at)
		if err != nil {
			return fmt.Errorf("--at %q: write a time as 2026-12-01T06:00:00Z", f.at)
		}
		d.ScheduledAt = &at
	}
	if f.acceptWarnings {
		d.AcceptWarnings = ptr(true)
	}
	if f.dryRun {
		return a.preview(ctx, c, d)
	}
	r, err := c.CreateRequestWithResponse(ctx, &rvavclient.CreateRequestParams{IdempotencyKey: uuid.New()}, d)
	if err != nil {
		return err
	}
	if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
		return err
	}
	return a.submitted(ctx, c, r.JSON202, r.Body, f.wait)
}

func (a *app) preview(ctx context.Context, c *rvavclient.ClientWithResponses, d rvavclient.RequestDraft) error {
	r, err := c.PreviewRequestWithResponse(ctx, &rvavclient.PreviewRequestParams{Validate: ptr(true)}, d)
	if err != nil {
		return err
	}
	if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
		return err
	}
	pv := r.JSON200
	if a.jsonOut() {
		if err := a.printJSON(rawJSON(r.Body)); err != nil {
			return err
		}
	} else if err := a.printPreview(pv); err != nil {
		return err
	}
	if !pv.Valid {
		return &problems{errors: pv.Errors}
	}
	return nil
}

func (a *app) printPreview(pv *rvavclient.RequestPreview) error {
	valid := "yes"
	if !pv.Valid {
		valid = "no"
	}
	approval := "not needed"
	if pv.Approval.Required {
		var names []string
		for _, p := range pv.Approval.Policies {
			names = append(names, p.Reason)
		}
		approval = "needed: " + strings.Join(names, "; ")
	}
	names := ""
	if pv.Names != nil {
		names = strings.Join(*pv.Names, ", ")
	}
	deployments := ""
	if pv.DeploymentNames != nil {
		deployments = strings.Join(*pv.DeploymentNames, ", ")
	}
	placement := ""
	if p := pv.Placement; p != nil {
		var parts []string
		for _, s := range []*string{p.Cluster, p.Datastore, p.Network, p.Folder} {
			if s != nil && *s != "" {
				parts = append(parts, *s)
			}
		}
		placement = strings.Join(parts, " · ")
	}
	var warnings []string
	if pv.Warnings != nil {
		for _, w := range *pv.Warnings {
			warnings = append(warnings, w.Message)
		}
	}
	return details(a.Out, "Valid", valid, "Deployments", deployments, "VMs", names, "Placement", placement,
		"Approval", approval, "Warnings", strings.Join(warnings, "; "))
}

// problems are a preview's errors, for the exit code.
type problems struct{ errors []rvavclient.FieldError }

func (p *problems) Error() string {
	lines := []string{"rvav would refuse this request:"}
	for _, e := range p.errors {
		if e.Field != "" {
			lines = append(lines, "  "+rvavclient.FieldName(e.Field)+": "+e.Message)
		} else {
			lines = append(lines, "  "+e.Message)
		}
	}
	return strings.Join(lines, "\n")
}
