package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/damhau/rvav/pkg/rvavclient"
)

var finalStatus = map[rvavclient.RequestStatus]bool{rvavclient.RequestStatusSucceeded: true,
	rvavclient.RequestStatusFailed: true, rvavclient.RequestStatusRejected: true, rvavclient.RequestStatusCancelled: true}

func (a *app) requestsCmd() *cobra.Command {
	c := group("requests", "Requests: follow, wait for, cancel, retry or signal them")
	c.AddCommand(a.requestsListCmd(), a.requestsShowCmd(), a.requestsWaitCmd(),
		a.requestActionCmd("cancel", "Cancel a request that is waiting or running; --wait fails unless it ends cancelled",
			rvavclient.RequestStatusCancelled, cancelRequest),
		a.requestActionCmd("retry", "Run a failed request again from its failed step", rvavclient.RequestStatusSucceeded,
			retryRequest),
		a.requestsSignalCmd())
	return c
}

func (a *app) requestsListCmd() *cobra.Command {
	var project, search string
	var status []string
	var mine bool
	var l listed
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
			reqs, raw, more, err := collect(l.max(), func(cursor *string, n int) (page[rvavclient.Request], error) {
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
			a.more(l, more)
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
	l.flags(c, "requests", 50)
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

// waitFlags are --wait and its --timeout.
type waitFlags struct {
	wait    bool
	timeout time.Duration
}

func (w *waitFlags) flags(c *cobra.Command) {
	c.Flags().BoolVarP(&w.wait, "wait", "w", false, "wait for the request to finish")
	w.timeoutFlag(c)
}

func (w *waitFlags) timeoutFlag(c *cobra.Command) {
	c.Flags().DurationVar(&w.timeout, "timeout", 0, "give up waiting after this long, for example 30m (never by default)")
}

// submitted prints a request just made, or waits for it to end as want.
func (a *app) submitted(ctx context.Context, c *rvavclient.ClientWithResponses, r *rvavclient.Request, body []byte, w waitFlags,
	want rvavclient.RequestStatus) error {
	if !w.wait {
		if a.jsonOut() {
			return a.printJSON(rawJSON(body))
		}
		return a.printRequest(r)
	}
	return a.wait(ctx, c, r.Id, w.timeout, want)
}

func (a *app) requestsWaitCmd() *cobra.Command {
	var w waitFlags
	c := &cobra.Command{
		Use:   "wait REQUEST",
		Short: "Wait for a request to finish; fail when it doesn't succeed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd.Context())
			if err != nil {
				return err
			}
			return a.wait(cmd.Context(), c, args[0], w.timeout, rvavclient.RequestStatusSucceeded)
		},
	}
	w.timeoutFlag(c)
	return c
}

// wait reads the request until it ends, saying how it goes on standard
// error, then prints it; a request that ends otherwise than want is an
// error. Errors worth trying again, such as a proxy's 502 during a rolling
// restart, don't end the wait.
func (a *app) wait(ctx context.Context, c *rvavclient.ClientWithResponses, id string, timeout time.Duration,
	want rvavclient.RequestStatus) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	name := id
	retry := &rvavclient.Retry{Interval: a.Poll, Retrying: func(err error, pause time.Duration) {
		fmt.Fprintf(a.Err, "%s: %s; trying again in %s\n", name, oneLine(err.Error(), 200), pause.Round(time.Second))
	}}
	var status rvavclient.RequestStatus
	step := ""
	for {
		r, err := a.getRequest(ctx, c, id)
		if err == nil {
			req := r.JSON200
			name = req.Number
			now := ""
			if req.CurrentStep != nil {
				now = req.CurrentStep.Title
			}
			switch {
			case status == "":
				fmt.Fprintf(a.Err, "%s (%s) %s\n", req.Number, req.Id, cell(progress(req.Status, now)))
			case req.Status != status || now != "" && now != step:
				fmt.Fprintf(a.Err, "%s %s\n", req.Number, cell(progress(req.Status, now)))
			}
			status = req.Status
			if now != "" {
				step = now
			}
			if finalStatus[req.Status] {
				return a.ended(req, r.Body, want)
			}
		}
		if err := retry.Wait(ctx, err); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
				return &timeoutError{fmt.Sprintf("%s hasn't finished after %s; rvav goes on with it", name, timeout)}
			}
			return err
		}
	}
}

// ended prints a request that ended, and fails unless it ended as want.
func (a *app) ended(req *rvavclient.Request, body []byte, want rvavclient.RequestStatus) error {
	var err error
	if a.jsonOut() {
		err = a.printJSON(rawJSON(body))
	} else {
		err = a.printRequest(req)
	}
	if err != nil || req.Status == want {
		return err
	}
	msg := fmt.Sprintf("%s %s", req.Number, req.Status)
	if req.Error != nil {
		msg += ": " + req.Error.Message
	}
	return &outcomeError{msg}
}

func progress(status rvavclient.RequestStatus, step string) string {
	if step == "" {
		return string(status)
	}
	return string(status) + ": " + step
}

// requestActionCmd is cancel or retry: a call on the request that
// answers with it, and --wait for the request to end as want.
func (a *app) requestActionCmd(use, short string, want rvavclient.RequestStatus,
	call func(ctx context.Context, c *rvavclient.ClientWithResponses, id string) (*http.Response, []byte, *rvavclient.Request, error)) *cobra.Command {
	var w waitFlags
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
			return a.submitted(ctx, c, r, body, w, want)
		},
	}
	w.flags(cmd)
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

func (a *app) requestsSignalCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "signal REQUEST NAME",
		Short: "Send the signal a composite step of a request waits for",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := a.signalData(data)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			r, err := c.SignalRequestWithResponse(ctx, args[0], args[1], body)
			if err != nil {
				return err
			}
			if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
				return err
			}
			if a.jsonOut() {
				return a.printJSON(rawJSON(r.Body))
			}
			_, err = fmt.Fprintf(a.Out, "%s took the signal %s.\n", strings.TrimPrefix(r.JSON202.Step, "step:"), args[1])
			return err
		},
	}
	cmd.Flags().StringVarP(&data, "data", "d", "", "what the signal brings, a JSON object; - reads it from standard input")
	return cmd
}

func (a *app) signalData(data string) (map[string]any, error) {
	if data == "-" {
		b, err := io.ReadAll(a.In)
		if err != nil {
			return nil, err
		}
		data = string(b)
	}
	body := map[string]any{}
	if strings.TrimSpace(data) == "" {
		return body, nil
	}
	if err := json.Unmarshal([]byte(data), &body); err != nil {
		return nil, &usageError{fmt.Errorf("--data: a JSON object: %w", err)}
	}
	return body, nil
}
