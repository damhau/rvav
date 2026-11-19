package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func (a *app) approvalsCmd() *cobra.Command {
	c := &cobra.Command{Use: "approvals", Short: "Approvals waiting for you, and deciding them"}
	c.AddCommand(a.approvalsListCmd(), a.approvalsShowCmd(), a.decideCmd("approve"), a.decideCmd("reject"))
	return c
}

func (a *app) approvalsListCmd() *cobra.Command {
	var status []string
	var all bool
	var limit int
	c := &cobra.Command{
		Use:   "list",
		Short: "Approvals waiting for you (the oldest first), or those you decided",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			params := rvavclient.ListApprovalsParams{}
			if all {
				params.View = ptr(rvavclient.ListApprovalsParamsView("all"))
			}
			if len(status) > 0 {
				st := make([]rvavclient.ApprovalStatus, len(status))
				for i, s := range status {
					st[i] = rvavclient.ApprovalStatus(s)
				}
				params.Status = &st
			}
			items, raw, err := collect(limit, func(cursor *string, n int) (page[rvavclient.ApprovalSummary], error) {
				p := params
				p.Limit, p.Cursor = &n, cursor
				r, err := c.ListApprovalsWithResponse(ctx, &p)
				if err != nil {
					return page[rvavclient.ApprovalSummary]{}, err
				}
				if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
					return page[rvavclient.ApprovalSummary]{}, err
				}
				return page[rvavclient.ApprovalSummary]{items: r.JSON200.Items, body: r.Body, next: r.JSON200.NextCursor}, nil
			})
			if err != nil {
				return err
			}
			return a.list(raw, "No approvals.", func() error {
				t := newTable(a.Out, "ID", "REQUEST", "TITLE", "LEVEL", "STATUS", "REQUESTER", "SUBMITTED", "DUE")
				for _, x := range items {
					due := when(x.DueAt)
					if x.Overdue {
						due += " (overdue)"
					}
					t.row(x.Id, x.Request.Number, x.Request.Title, levelName(x.Level.Name, x.Level.Index, x.Level.Count),
						string(x.Status), x.Requester.Name, when(&x.SubmittedAt), due)
				}
				return t.flush()
			})
		},
	}
	c.Flags().StringSliceVar(&status, "status", nil, "only these statuses, for example approved,rejected (pending by default)")
	c.Flags().BoolVar(&all, "all", false, "every approval, for administrators and auditors")
	c.Flags().IntVar(&limit, "limit", 50, "at most this many approvals")
	return c
}

func levelName(name string, index, count int) string {
	if count <= 1 {
		return name
	}
	return fmt.Sprintf("%s (%d of %d)", name, index, count)
}

func (a *app) getApproval(ctx context.Context, c *rvavclient.ClientWithResponses, id string) (*rvavclient.GetApprovalResponse, error) {
	r, err := c.GetApprovalWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	return r, rvavclient.Check(r.HTTPResponse, r.Body)
}

func (a *app) approvalsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show APPROVAL",
		Short: "An approval with what you need to decide it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			r, err := a.getApproval(ctx, c, args[0])
			if err != nil {
				return err
			}
			if a.jsonOut() {
				return a.printJSON(rawJSON(r.Body))
			}
			return a.printApproval(r.JSON200)
		},
	}
}

func (a *app) printApproval(x *rvavclient.Approval) error {
	decide := "no"
	if x.CanDecide {
		decide = "yes"
	}
	approvers := make([]string, len(x.Approvers))
	for i, u := range x.Approvers {
		approvers[i] = u.Name
	}
	pairs := []string{"Approval", x.Id, "Request", x.Request.Number + " (" + x.Request.Id + "): " + x.Request.Title,
		"Status", string(x.Status), "Level", levelName(x.Level.Name, x.Level.Index, x.Level.Count),
		"Project", x.Project.Name, "Environment", x.Environment, "Requester", x.Requester.Name,
		"Submitted", when(&x.SubmittedAt), "Due", when(x.DueAt), "Why", strings.Join(x.Reasons, "; "),
		"Justification", str(x.Justification), "Approvers", strings.Join(approvers, ", "),
		"Outcome", str(x.Outcome), "You may decide", decide}
	for _, f := range x.Facts {
		pairs = append(pairs, f.Label, f.Value)
	}
	if err := details(a.Out, pairs...); err != nil {
		return err
	}
	if len(x.EditableFields) == 0 {
		return nil
	}
	fmt.Fprintln(a.Out)
	t := newTable(a.Out, "YOU MAY CHANGE", "LABEL", "VALUE", "CHOICES")
	for _, f := range x.EditableFields {
		var choices []string
		if f.Options != nil {
			for _, o := range *f.Options {
				choices = append(choices, o.Value)
			}
		}
		if f.Minimum != nil || f.Maximum != nil {
			choices = append(choices, fmt.Sprintf("%s to %s", bound(f.Minimum), bound(f.Maximum)))
		}
		t.row(f.Field, f.Label, shown(f.Value), strings.Join(choices, ","))
	}
	return t.flush()
}

func bound(n *float32) string {
	if n == nil {
		return "any"
	}
	return strconv.FormatFloat(float64(*n), 'f', -1, 32)
}

func (a *app) decideCmd(decision string) *cobra.Command {
	var comment string
	var inputs []string
	short := "Approve a request at this level, changing the inputs approvers may"
	if decision == "reject" {
		short = "Reject a request, saying why"
	}
	c := &cobra.Command{
		Use:   decision + " APPROVAL",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if decision == "reject" && comment == "" {
				return errors.New("say why with --comment: the requester gets it")
			}
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			d := rvavclient.ApprovalDecision{Decision: rvavclient.ApprovalDecisionDecision(decision), Comment: optional(comment)}
			if len(inputs) > 0 {
				ar, err := a.getApproval(ctx, c, args[0])
				if err != nil {
					return err
				}
				changed, err := (inputFlags{values: inputs}).read(a.In, editableSchema(ar.JSON200.EditableFields))
				if err != nil {
					return err
				}
				d.Inputs = &changed
			}
			r, err := c.DecideApprovalWithResponse(ctx, args[0], d)
			if err != nil {
				return err
			}
			if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
				return err
			}
			if a.jsonOut() {
				return a.printJSON(rawJSON(r.Body))
			}
			return a.printApproval(r.JSON200)
		},
	}
	c.Flags().StringVarP(&comment, "comment", "m", "", "a comment for the requester (needed to reject)")
	if decision == "approve" {
		c.Flags().StringArrayVarP(&inputs, "input", "i", nil, "a new value for an input approvers may change, name=value")
	}
	return c
}

// editableSchema types the inputs an approver may change from their
// current values: numbers stay numbers.
func editableSchema(fields []rvavclient.ApprovalEditableField) map[string]any {
	props := map[string]any{}
	for _, f := range fields {
		t := "string"
		switch f.Value.(type) {
		case float64:
			t = "number"
		case bool:
			t = "boolean"
		case []any:
			t = "array"
		}
		if f.Minimum != nil || f.Maximum != nil {
			t = "number"
		}
		props[f.Field] = map[string]any{"type": t}
	}
	return map[string]any{"properties": props}
}
