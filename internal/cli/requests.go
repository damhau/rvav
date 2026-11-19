package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/damhau/rvav/pkg/rvavclient"
)

var finalStatus = map[rvavclient.RequestStatus]bool{rvavclient.RequestStatusSucceeded: true,
	rvavclient.RequestStatusFailed: true, rvavclient.RequestStatusRejected: true, rvavclient.RequestStatusCancelled: true}

func (a *app) requestsCmd() *cobra.Command {
	c := &cobra.Command{Use: "requests", Short: "Requests: follow, wait for, cancel or retry them"}
	c.AddCommand(a.requestsListCmd(), a.requestsShowCmd(), a.requestsWaitCmd(),
		a.requestActionCmd("cancel", "Cancel a request that is waiting or running", cancelRequest),
		a.requestActionCmd("retry", "Run a failed request again from its failed step", retryRequest))
	return c
}

func (a *app) requestsListCmd() *cobra.Command {
	var project, search string
	var status []string
	var mine bool
	var limit int
	c := &cobra.Command{
		Use:   "list",
		Short: "Requests you can see, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			params := rvavclient.ListRequestsParams{Q: optional(search)}
			if project != "" {
				pid, err := a.project(ctx, c, project)
				if err != nil {
					return err
				}
				params.ProjectId = &pid
			}
			if len(status) > 0 {
				st := make([]rvavclient.RequestStatus, len(status))
				for i, s := range status {
					st[i] = rvavclient.RequestStatus(s)
				}
				params.Status = &st
			}
			if mine {
				params.Requester = ptr("me")
			}
			reqs, raw, err := collect(limit, func(cursor *string, n int) (page[rvavclient.Request], error) {
				p := params
				p.Limit, p.Cursor = &n, cursor
				r, err := c.ListRequestsWithResponse(ctx, &p)
				if err != nil {
					return page[rvavclient.Request]{}, err
				}
				if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
					return page[rvavclient.Request]{}, err
				}
				return page[rvavclient.Request]{items: r.JSON200.Items, body: r.Body, next: r.JSON200.NextCursor}, nil
			})
			if err != nil {
				return err
			}
			return a.list(raw, "No requests.", func() error {
				t := newTable(a.Out, "ID", "NUMBER", "TITLE", "STATUS", "PROJECT", "ENV", "REQUESTER", "SUBMITTED")
				for _, r := range reqs {
					t.row(r.Id, r.Number, r.Title, string(r.Status), r.Project.Code, r.Environment, r.Requester.Name,
						when(&r.SubmittedAt))
				}
				return t.flush()
			})
		},
	}
	c.Flags().StringVarP(&project, "project", "p", "", "one project's requests, by ID, code or name")
	c.Flags().StringSliceVar(&status, "status", nil, "only these statuses, for example running,failed")
	c.Flags().BoolVar(&mine, "mine", false, "only those you made")
	c.Flags().StringVarP(&search, "search", "q", "", "requests whose number or title has this text")
	c.Flags().IntVar(&limit, "limit", 50, "at most this many requests")
	return c
}

func (a *app) getRequest(ctx context.Context, c *rvavclient.ClientWithResponses, id string) (*rvavclient.GetRequestResponse, error) {
	r, err := c.GetRequestWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	return r, rvavclient.Check(r.HTTPResponse, r.Body)
}

func (a *app) requestsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show REQUEST",
		Short: "A request and its steps",
		Long: `A request and its steps. With -o json, the request and its steps as the API
returns them ({"request": ..., "steps": [...]}).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			r, err := a.getRequest(ctx, c, args[0])
			if err != nil {
				return err
			}
			s, err := c.ListRequestStepsWithResponse(ctx, args[0])
			if err != nil {
				return err
			}
			if err := rvavclient.Check(s.HTTPResponse, s.Body); err != nil {
				return err
			}
			if a.jsonOut() {
				steps, err := rawItems(s.Body)
				if err != nil {
					return err
				}
				return a.printJSON(map[string]any{"request": rawJSON(r.Body), "steps": steps})
			}
			if err := a.printRequest(r.JSON200); err != nil {
				return err
			}
			if len(s.JSON200.Items) == 0 {
				return nil
			}
			fmt.Fprintln(a.Out)
			t := newTable(a.Out, "STEP", "STATUS", "STARTED", "FINISHED", "NOTE")
			var walk func(steps []rvavclient.Step, depth int)
			walk = func(steps []rvavclient.Step, depth int) {
				for _, st := range steps {
					note := str(st.Error)
					if note == "" {
						note = str(st.Detail)
					}
					t.row(strings.Repeat("  ", depth)+st.Title, string(st.Status), when(st.StartedAt), when(st.FinishedAt), note)
					walk(st.Children, depth+1)
				}
			}
			walk(s.JSON200.Items, 0)
			return t.flush()
		},
	}
}

func (a *app) printRequest(r *rvavclient.Request) error {
	item := r.CatalogItem.Name + " " + r.Version.Number
	if r.Action != nil {
		item = r.Action.Name + " (" + r.CatalogItem.Name + ")"
	}
	progress := ""
	if r.Progress.Total > 0 {
		progress = fmt.Sprintf("%d of %d steps", r.Progress.Done, r.Progress.Total)
	}
	current, failure := "", ""
	if r.CurrentStep != nil {
		current = r.CurrentStep.Title
	}
	if r.Error != nil {
		failure = r.Error.Message
	}
	deployments := ""
	if r.DeploymentIds != nil {
		deployments = strings.Join(*r.DeploymentIds, ", ")
	} else if r.DeploymentId != nil {
		deployments = *r.DeploymentId
	}
	return details(a.Out, "Request", r.Number+" ("+r.Id+")", "Title", r.Title, "Status", string(r.Status),
		"Kind", string(r.Kind), "Item", item, "Project", r.Project.Name, "Environment", r.Environment,
		"Requester", r.Requester.Name, "Submitted", when(&r.SubmittedAt), "Scheduled", when(r.ScheduledAt),
		"Started", when(r.StartedAt), "Finished", when(r.FinishedAt), "Progress", progress, "Current step", current,
		"Error", failure, "Deployments", deployments)
}

// submitted prints a request just made, or waits for it.
func (a *app) submitted(ctx context.Context, c *rvavclient.ClientWithResponses, r *rvavclient.Request, body []byte, wait bool) error {
	if !wait {
		if a.jsonOut() {
			return a.printJSON(rawJSON(body))
		}
		return a.printRequest(r)
	}
	return a.wait(ctx, c, r.Id, 0)
}

func (a *app) requestsWaitCmd() *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "wait REQUEST",
		Short: "Wait for a request to finish; fail when it doesn't succeed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd.Context())
			if err != nil {
				return err
			}
			return a.wait(cmd.Context(), c, args[0], timeout)
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long, for example 30m (never by default)")
	return c
}

// wait reads the request until it ends, saying how it goes on standard
// error, then prints it; a request that didn't succeed is an error.
func (a *app) wait(ctx context.Context, c *rvavclient.ClientWithResponses, id string, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var status rvavclient.RequestStatus
	step := ""
	for {
		r, err := a.getRequest(ctx, c, id)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("%s hasn't finished after %s", id, timeout)
			}
			return err
		}
		req := r.JSON200
		now := ""
		if req.CurrentStep != nil {
			now = req.CurrentStep.Title
		}
		switch {
		case status == "":
			fmt.Fprintf(a.Err, "%s (%s) %s\n", req.Number, req.Id, progress(req.Status, now))
		case req.Status != status || now != "" && now != step:
			fmt.Fprintf(a.Err, "%s %s\n", req.Number, progress(req.Status, now))
		}
		status = req.Status
		if now != "" {
			step = now
		}
		if finalStatus[req.Status] {
			if a.jsonOut() {
				err = a.printJSON(rawJSON(r.Body))
			} else {
				err = a.printRequest(req)
			}
			if err != nil {
				return err
			}
			if req.Status != rvavclient.RequestStatusSucceeded {
				msg := fmt.Sprintf("%s %s", req.Number, req.Status)
				if req.Error != nil {
					msg += ": " + req.Error.Message
				}
				return fmt.Errorf("%s", msg)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("%s hasn't finished after %s", req.Number, timeout)
			}
			return ctx.Err()
		case <-time.After(a.Poll):
		}
	}
}

func progress(status rvavclient.RequestStatus, step string) string {
	if step == "" {
		return string(status)
	}
	return string(status) + ": " + step
}

// requestActionCmd is cancel or retry: a call on the request that
// answers with it.
func (a *app) requestActionCmd(use, short string,
	call func(ctx context.Context, c *rvavclient.ClientWithResponses, id string) (*http.Response, []byte, *rvavclient.Request, error)) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   use + " REQUEST",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			resp, body, r, err := call(ctx, c, args[0])
			if err != nil {
				return err
			}
			if err := rvavclient.Check(resp, body); err != nil {
				return err
			}
			return a.submitted(ctx, c, r, body, wait)
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "wait for the request to finish")
	return cmd
}

func cancelRequest(ctx context.Context, c *rvavclient.ClientWithResponses, id string) (*http.Response, []byte, *rvavclient.Request, error) {
	r, err := c.CancelRequestWithResponse(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	return r.HTTPResponse, r.Body, r.JSON202, nil
}

func retryRequest(ctx context.Context, c *rvavclient.ClientWithResponses, id string) (*http.Response, []byte, *rvavclient.Request, error) {
	r, err := c.RetryRequestWithResponse(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	return r.HTTPResponse, r.Body, r.JSON202, nil
}
