package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func (a *app) deploymentsCmd() *cobra.Command {
	c := &cobra.Command{Use: "deployments", Short: "Deployments and their day-2 actions"}
	c.AddCommand(a.deploymentsListCmd(), a.deploymentsShowCmd(), a.deploymentsActionsCmd(), a.deploymentsRunCmd())
	return c
}

func (a *app) deploymentsListCmd() *cobra.Command {
	var project, search string
	var status, envs []string
	var mine bool
	var limit int
	c := &cobra.Command{
		Use:   "list",
		Short: "Deployments you can see, by name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			params := rvavclient.ListDeploymentsParams{Q: optional(search)}
			if project != "" {
				pid, err := a.project(ctx, c, project)
				if err != nil {
					return err
				}
				params.ProjectId = &pid
			}
			if len(status) > 0 {
				st := make([]rvavclient.DeploymentStatusFilter, len(status))
				for i, s := range status {
					st[i] = rvavclient.DeploymentStatusFilter(s)
				}
				params.Status = &st
			}
			if len(envs) > 0 {
				params.Environment = &envs
			}
			if mine {
				params.Owner = ptr("me")
			}
			deps, raw, err := collect(limit, func(cursor *string, n int) (page[rvavclient.Deployment], error) {
				p := params
				p.Limit, p.Cursor = &n, cursor
				r, err := c.ListDeploymentsWithResponse(ctx, &p)
				if err != nil {
					return page[rvavclient.Deployment]{}, err
				}
				if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
					return page[rvavclient.Deployment]{}, err
				}
				return page[rvavclient.Deployment]{items: r.JSON200.Items, body: r.Body, next: r.JSON200.NextCursor}, nil
			})
			if err != nil {
				return err
			}
			return a.list(raw, "No deployments.", func() error {
				t := newTable(a.Out, "ID", "NAME", "STATUS", "PROJECT", "ENV", "ITEM", "OWNER", "VMS", "LEASE ENDS")
				for _, d := range deps {
					t.row(d.Id, d.Name, string(d.Status), d.Project.Code, d.Environment, d.CatalogItem.Name, d.Owner.Name,
						fmt.Sprintf("%d/%d on", d.VmsPoweredOn, d.VmCount), when(d.Lease.ExpiresAt))
				}
				return t.flush()
			})
		},
	}
	c.Flags().StringVarP(&project, "project", "p", "", "one project's deployments, by ID, code or name")
	c.Flags().StringSliceVar(&status, "status", nil, "only these statuses, for example failed,expiring")
	c.Flags().StringSliceVarP(&envs, "env", "e", nil, "only these environments, by key")
	c.Flags().BoolVar(&mine, "mine", false, "only those you own")
	c.Flags().StringVarP(&search, "search", "q", "", "a deployment's name, its owner's, or a VM's name, address or FQDN")
	c.Flags().IntVar(&limit, "limit", 200, "at most this many deployments")
	return c
}

// resourceRow is what a deployment's resources table shows of each.
type resourceRow struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	VM   *struct {
		PowerState string `json:"power_state"`
		CPUCount   int    `json:"cpu_count"`
		MemoryMB   int    `json:"memory_mb"`
		FQDN       string `json:"fqdn"`
		NICs       []struct {
			IPs []string `json:"ip_addresses"`
		} `json:"nics"`
	} `json:"vm"`
	Address string `json:"address"`
}

func (r resourceRow) detail() string {
	if r.VM == nil {
		return r.Address
	}
	parts := []string{r.VM.PowerState, fmt.Sprintf("%d vCPU", r.VM.CPUCount), fmt.Sprintf("%g GB", float64(r.VM.MemoryMB)/1024)}
	if len(r.VM.NICs) > 0 && len(r.VM.NICs[0].IPs) > 0 {
		parts = append(parts, r.VM.NICs[0].IPs[0])
	}
	if r.VM.FQDN != "" {
		parts = append(parts, r.VM.FQDN)
	}
	return strings.Join(parts, " · ")
}

func (a *app) deploymentsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show DEPLOYMENT",
		Short: "A deployment and its resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			r, err := c.GetDeploymentWithResponse(ctx, args[0])
			if err != nil {
				return err
			}
			if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
				return err
			}
			if a.jsonOut() {
				return a.printJSON(rawJSON(r.Body))
			}
			d := r.JSON200
			address := ""
			if d.PrimaryAddress != nil {
				address = d.PrimaryAddress.Ip
				if d.PrimaryAddress.Fqdn != nil {
					address += " (" + *d.PrimaryAddress.Fqdn + ")"
				}
			}
			lease := when(d.Lease.ExpiresAt)
			if d.Lease.ExpiresAt == nil {
				lease = "none"
			}
			if err := details(a.Out, "Deployment", d.Name+" ("+d.Id+")", "Description", d.Description,
				"Status", string(d.Status), "Project", d.Project.Name, "Environment", d.Environment,
				"Item", d.CatalogItem.Name+" "+d.Version.Number, "Owner", d.Owner.Name, "Created", when(&d.CreatedAt),
				"Lease ends", lease, "Address", address); err != nil {
				return err
			}
			var res struct {
				Resources []resourceRow `json:"resources"`
			}
			if err := json.Unmarshal(r.Body, &res); err != nil || len(res.Resources) == 0 {
				return err
			}
			fmt.Fprintln(a.Out)
			t := newTable(a.Out, "RESOURCE", "TYPE", "NAME", "STATE")
			for _, x := range res.Resources {
				t.row(x.ID, x.Type, x.Name, x.detail())
			}
			return t.flush()
		},
	}
}

func (a *app) actions(ctx context.Context, c *rvavclient.ClientWithResponses, id string) (*rvavclient.ListDeploymentActionsResponse, error) {
	r, err := c.ListDeploymentActionsWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	return r, rvavclient.Check(r.HTTPResponse, r.Body)
}

func (a *app) deploymentsActionsCmd() *cobra.Command {
	var resource string
	c := &cobra.Command{
		Use:   "actions DEPLOYMENT [ACTION]",
		Short: "The day-2 actions of a deployment and its resources, and whether you may run each now",
		Long: `The day-2 actions of a deployment and its resources, and whether you may run
each now. With an action's key, that action and the inputs its form takes.`,
		Example: `  rvavctl deployments actions dep_01J...
  rvavctl deployments actions dep_01J... resize --resource web-dev-app001`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			r, err := a.actions(ctx, c, args[0])
			if err != nil {
				return err
			}
			if len(args) == 2 {
				x, err := pickAction(r.JSON200.Items, args[1], resource)
				if err != nil {
					return err
				}
				if a.jsonOut() {
					return a.printJSON(x)
				}
				return a.printAction(x)
			}
			raw, err := rawItems(r.Body)
			if err != nil {
				return err
			}
			return a.list(raw, "No actions.", func() error {
				t := newTable(a.Out, "ACTION", "NAME", "RESOURCE", "AVAILABLE", "APPROVAL")
				for _, x := range r.JSON200.Items {
					t.row(x.Key, x.Name, str(x.ResourceName), available(x), approvalNeeded(x))
				}
				return t.flush()
			})
		},
	}
	c.Flags().StringVarP(&resource, "resource", "r", "", "the resource the action is on, by name or ID")
	return c
}

func available(x rvavclient.Day2Action) string {
	if !x.Enabled {
		return "no: " + str(x.DisabledReason)
	}
	return "yes"
}

func approvalNeeded(x rvavclient.Day2Action) string {
	if !x.Approval.Required {
		return ""
	}
	if len(x.Approval.Reasons) > 0 {
		return "needed: " + strings.Join(x.Approval.Reasons, "; ")
	}
	return "needed"
}

func (a *app) printAction(x rvavclient.Day2Action) error {
	confirm := ""
	if x.Confirm != nil {
		confirm = x.Confirm.Message + " (--yes)"
	} else if x.Destructive {
		confirm = "--yes"
	}
	var warnings []string
	if x.Warnings != nil {
		for _, w := range *x.Warnings {
			warnings = append(warnings, w.Message)
		}
	}
	if err := details(a.Out, "Action", x.Key, "Name", x.Name, "Resource", str(x.ResourceName), "Available", available(x),
		"Approval", approvalNeeded(x), "Confirm", confirm, "Warnings", strings.Join(warnings, "; ")); err != nil {
		return err
	}
	if x.Form == nil {
		return nil
	}
	_, err := a.printInputs(x.Form.Schema, x.Form.UiSchema, x.Form.InitialValues, "(rvav lists them)")
	return err
}

type runFlags struct {
	resource, justification string
	acceptWarnings, yes     bool
	wait                    bool
	inputs                  inputFlags
}

func (a *app) deploymentsRunCmd() *cobra.Command {
	var f runFlags
	c := &cobra.Command{
		Use:   "run DEPLOYMENT ACTION",
		Short: "Run a day-2 action on a deployment or one of its resources",
		Long: `Run a day-2 action, by its key (see rvavctl deployments actions). An action on
a VM needs --resource, the VM's name or resource ID, when the deployment has
several. Inputs the action's form doesn't get take its defaults. An action
that deletes or can't be undone needs --yes.`,
		Example: `  rvavctl deployments run dep_01J... power_off --resource web-dev-app001 --wait
  rvavctl deployments run dep_01J... snapshot_create --input name=before-upgrade
  rvavctl deployments run dep_01J... delete --yes`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return a.run(cmd.Context(), args[0], args[1], f) },
	}
	fl := c.Flags()
	fl.StringVarP(&f.resource, "resource", "r", "", "the resource it acts on, by name or ID")
	fl.StringArrayVarP(&f.inputs.values, "input", "i", nil, "an input, name=value (repeat it)")
	fl.StringVar(&f.inputs.file, "inputs", "", "a JSON or YAML file of inputs, - for standard input")
	fl.StringVar(&f.justification, "justification", "", "why, for the approvers")
	fl.BoolVar(&f.acceptWarnings, "accept-warnings", false, "go on despite soft policies' warnings")
	fl.BoolVarP(&f.yes, "yes", "y", false, "confirm an action that deletes or can't be undone")
	fl.BoolVarP(&f.wait, "wait", "w", false, "wait for the request to finish")
	return c
}

func (a *app) run(ctx context.Context, deployment, key string, f runFlags) error {
	c, err := a.connect(ctx)
	if err != nil {
		return err
	}
	r, err := a.actions(ctx, c, deployment)
	if err != nil {
		return err
	}
	action, err := pickAction(r.JSON200.Items, key, f.resource)
	if err != nil {
		return err
	}
	if !action.Enabled {
		return fmt.Errorf("%s isn't available: %s", action.Name, str(action.DisabledReason))
	}
	if (action.Destructive || action.Confirm != nil) && !f.yes {
		why := action.Name + " deletes or can't be undone"
		if action.Confirm != nil {
			why = action.Confirm.Message
		}
		return fmt.Errorf("%s\nAdd --yes to go on", why)
	}
	var schema, initial map[string]any
	if action.Form != nil {
		schema, initial = action.Form.Schema, action.Form.InitialValues
	}
	given, err := f.inputs.read(a.In, schema)
	if err != nil {
		return err
	}
	d := rvavclient.Day2Draft{Action: action.Key, ResourceId: action.ResourceId, Inputs: withInitial(initial, given),
		Justification: optional(f.justification)}
	if f.acceptWarnings {
		d.AcceptWarnings = ptr(true)
	}
	resp, err := c.RunDeploymentActionWithResponse(ctx, deployment,
		&rvavclient.RunDeploymentActionParams{IdempotencyKey: uuid.New()}, d)
	if err != nil {
		return err
	}
	if err := rvavclient.Check(resp.HTTPResponse, resp.Body); err != nil {
		return err
	}
	return a.submitted(ctx, c, resp.JSON202, resp.Body, f.wait)
}

// pickAction is the action key names, on the resource given or the only
// one it applies to.
func pickAction(all []rvavclient.Day2Action, key, resource string) (rvavclient.Day2Action, error) {
	var matches []rvavclient.Day2Action
	for _, x := range all {
		if x.Key != key {
			continue
		}
		if resource == "" || str(x.ResourceId) == resource || strings.EqualFold(str(x.ResourceName), resource) {
			matches = append(matches, x)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = str(m.ResourceName)
		}
		return rvavclient.Day2Action{}, fmt.Errorf("%s applies to several resources: give --resource, one of %s",
			key, strings.Join(names, ", "))
	case resource != "":
		return rvavclient.Day2Action{}, fmt.Errorf("no action %s on resource %s: see rvavctl deployments actions", key, resource)
	}
	return rvavclient.Day2Action{}, fmt.Errorf("no action %s on this deployment: see rvavctl deployments actions", key)
}
