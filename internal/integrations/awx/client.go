package awx

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/observe"
)

// HTTPClient talks to the AWX REST API (/api/v2) with an OAuth2 token.
type HTTPClient struct {
	base  *url.URL
	token string
	http  *http.Client
	// Organization limits template lookups to one organisation.
	Organization string
}

// APIError is an error AWX answered with.
type APIError struct {
	Method, Path string
	Status       int
	Message      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("awx %s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
}

// errorMessage reads AWX's error body: {"detail": "..."}, or field errors
// such as {"extra_vars": ["..."], "variables_needed_to_start": ["..."]}.
func errorMessage(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return strings.TrimSpace(string(body[:min(len(body), 300)]))
	}
	if d, ok := m["detail"].(string); ok {
		return d
	}
	var parts []string
	for k, v := range m {
		switch v := v.(type) {
		case []any:
			for _, x := range v {
				parts = append(parts, fmt.Sprintf("%s: %v", k, x))
			}
		default:
			parts = append(parts, fmt.Sprintf("%s: %v", k, v))
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, "; ")
}

func NewHTTPClient(rawURL, token string, insecure bool) (*HTTPClient, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("awx url %q is not valid", rawURL)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for lab AWX
	}
	return &HTTPClient{base: u, token: token, http: &http.Client{Transport: observe.Transport("awx", tr), Timeout: 30 * time.Second}}, nil
}

func (c *HTTPClient) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/v2" + path
	u.RawQuery = query.Encode()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("awx %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: errorMessage(data)}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("awx %s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

func kindPath(workflow bool) (templates, jobs string) {
	if workflow {
		return "/workflow_job_templates", "/workflow_jobs"
	}
	return "/job_templates", "/jobs"
}

func (c *HTTPClient) LookupJobTemplate(ctx context.Context, name string, workflow bool) (int, error) {
	tpl, _ := kindPath(workflow)
	var res struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	q := url.Values{"name": {name}}
	if c.Organization != "" {
		q.Set("organization__name", c.Organization)
	}
	if err := c.do(ctx, http.MethodGet, tpl+"/", q, nil, &res); err != nil {
		return 0, err
	}
	if len(res.Results) == 0 {
		if c.Organization != "" {
			return 0, fmt.Errorf("awx template %q not found in organization %s", name, c.Organization)
		}
		return 0, fmt.Errorf("awx template %q not found", name)
	}
	return res.Results[0].ID, nil
}

type jobJSON struct {
	ID       int    `json:"id"`
	Job      int    `json:"job"`
	Workflow int    `json:"workflow_job"`
	Status   string `json:"status"`
	Failed   bool   `json:"failed"`
}

func (c *HTTPClient) toJob(j jobJSON, workflow bool) Job {
	id := j.ID
	if j.Job != 0 {
		id = j.Job
	}
	if j.Workflow != 0 {
		id = j.Workflow
	}
	kind := "playbook"
	if workflow {
		kind = "workflow"
	}
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/"
	u.Fragment = fmt.Sprintf("/jobs/%s/%d/output", kind, id)
	return Job{ID: id, Status: j.Status, Failed: j.Failed, Finished: Done(j.Status), URL: u.String()}
}

// Launch starts a job. A limit or an inventory are checked first to be
// prompted on launch: AWX ignores what it doesn't prompt for and launches
// anyway, so a limit would be dropped (the job runs on every host) and an
// inventory too (the limit could match another host of the same name).
// Should AWX ignore them all the same, the job is cancelled at once.
// Variables not prompted for are dropped by AWX, except survey answers.
func (c *HTTPClient) Launch(ctx context.Context, templateID int, workflow bool, req LaunchRequest) (Job, error) {
	tpl, _ := kindPath(workflow)
	body := map[string]any{"extra_vars": req.ExtraVars}
	if req.Limit != "" || req.Inventory != 0 || len(req.Credentials) > 0 {
		var prompts struct {
			AskLimit      bool `json:"ask_limit_on_launch"`
			AskInventory  bool `json:"ask_inventory_on_launch"`
			AskCredential bool `json:"ask_credential_on_launch"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/%d/launch/", tpl, templateID), nil, nil, &prompts); err != nil {
			return Job{}, err
		}
		if req.Limit != "" && !prompts.AskLimit {
			return Job{}, fmt.Errorf("awx template %d: %w", templateID, ErrNoLimitPrompt)
		}
		if req.Inventory != 0 && !prompts.AskInventory {
			return Job{}, fmt.Errorf("awx template %d: %w", templateID, ErrNoInventoryPrompt)
		}
		if len(req.Credentials) > 0 && !prompts.AskCredential {
			return Job{}, fmt.Errorf("awx template %d: %w", templateID, ErrNoCredentialPrompt)
		}
	}
	if len(req.Credentials) > 0 {
		body["credentials"] = req.Credentials
	}
	if req.Limit != "" {
		body["limit"] = req.Limit
	}
	if req.Inventory != 0 {
		body["inventory"] = req.Inventory
	}
	var j struct {
		jobJSON
		Ignored map[string]any `json:"ignored_fields"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/launch/", tpl, templateID), nil, body, &j); err != nil {
		return Job{}, err
	}
	job := c.toJob(j.jobJSON, workflow)
	for _, f := range []string{"limit", "inventory", "credentials"} {
		if _, ignored := j.Ignored[f]; ignored {
			_ = c.Cancel(context.WithoutCancel(ctx), job.ID, workflow)
			return Job{}, fmt.Errorf("awx job %d of template %d: %w (%s), so it was cancelled", job.ID, templateID, ErrPromptIgnored, f)
		}
	}
	return job, nil
}

func (c *HTTPClient) GetJob(ctx context.Context, id int, workflow bool) (Job, error) {
	_, jobs := kindPath(workflow)
	var j jobJSON
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/%d/", jobs, id), nil, nil, &j); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return Job{}, fmt.Errorf("awx job %d: %w", id, ErrJobNotFound)
		}
		return Job{}, err
	}
	return c.toJob(j, workflow), nil
}

func (c *HTTPClient) Events(ctx context.Context, jobID int, after int) ([]Event, error) {
	var out []Event
	q := url.Values{"counter__gt": {fmt.Sprint(after)}, "order_by": {"counter"}, "page_size": {"200"}}
	for page := 1; page < 50; page++ {
		q.Set("page", fmt.Sprint(page))
		var res struct {
			Next    *string `json:"next"`
			Results []struct {
				Counter   int    `json:"counter"`
				Event     string `json:"event"`
				Task      string `json:"task"`
				HostName  string `json:"host_name"`
				Stdout    string `json:"stdout"`
				Changed   bool   `json:"changed"`
				Failed    bool   `json:"failed"`
				EventData struct {
					Res struct {
						Msg any `json:"msg"`
					} `json:"res"`
				} `json:"event_data"`
			} `json:"results"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/jobs/%d/job_events/", jobID), q, nil, &res); err != nil {
			return nil, err
		}
		for _, r := range res.Results {
			e := Event{Counter: r.Counter, Event: r.Event, Task: r.Task, Host: r.HostName, Stdout: r.Stdout,
				Changed: r.Changed, Failed: r.Failed}
			if r.EventData.Res.Msg != nil {
				e.Message = fmt.Sprint(r.EventData.Res.Msg)
			}
			out = append(out, e)
		}
		if res.Next == nil {
			break
		}
	}
	return out, nil
}

func (c *HTTPClient) Cancel(ctx context.Context, jobID int, workflow bool) error {
	_, jobs := kindPath(workflow)
	return c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/cancel/", jobs, jobID), nil, map[string]any{}, nil)
}

func (c *HTTPClient) UpdateInventorySource(ctx context.Context, sourceID int) (int, error) {
	var res struct {
		ID              int `json:"id"`
		InventoryUpdate int `json:"inventory_update"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/inventory_sources/%d/update/", sourceID), nil, map[string]any{}, &res); err != nil {
		return 0, err
	}
	if res.InventoryUpdate != 0 {
		return res.InventoryUpdate, nil
	}
	return res.ID, nil
}

func (c *HTTPClient) InventoryOfSource(ctx context.Context, sourceID int) (int, error) {
	var res struct {
		Inventory int `json:"inventory"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/inventory_sources/%d/", sourceID), nil, nil, &res); err != nil {
		return 0, err
	}
	return res.Inventory, nil
}

// list reads every page of a list (200 objects a page, 50 pages at most)
// into add.
func (c *HTTPClient) list(ctx context.Context, path string, q url.Values, add func(raw json.RawMessage) error) error {
	q.Set("page_size", "200")
	for page := 1; page <= 50; page++ {
		q.Set("page", fmt.Sprint(page))
		var res struct {
			Next    *string           `json:"next"`
			Results []json.RawMessage `json:"results"`
		}
		if err := c.do(ctx, http.MethodGet, path, q, nil, &res); err != nil {
			return err
		}
		for _, r := range res.Results {
			if err := add(r); err != nil {
				return err
			}
		}
		if res.Next == nil {
			return nil
		}
	}
	return nil
}

func (c *HTTPClient) Inventories(ctx context.Context, organization string) ([]Inventory, error) {
	q := url.Values{"order_by": {"name"}}
	if organization != "" {
		q.Set("organization__name", organization)
	}
	out := []Inventory{}
	err := c.list(ctx, "/inventories/", q, func(raw json.RawMessage) error {
		var i Inventory
		if err := json.Unmarshal(raw, &struct {
			ID   *int    `json:"id"`
			Name *string `json:"name"`
			Kind *string `json:"kind"`
		}{&i.ID, &i.Name, &i.Kind}); err != nil {
			return err
		}
		out = append(out, i)
		return nil
	})
	return out, err
}

func (c *HTTPClient) Credentials(ctx context.Context, organization, kind string) ([]Credential, error) {
	q := url.Values{"order_by": {"name"}}
	if organization != "" {
		q.Set("organization__name", organization)
	}
	if kind != "" {
		q.Set("credential_type__kind", kind)
	}
	out := []Credential{}
	err := c.list(ctx, "/credentials/", q, func(raw json.RawMessage) error {
		var r struct {
			ID      int    `json:"id"`
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			Summary struct {
				Type struct {
					Name string `json:"name"`
					Kind string `json:"kind"`
				} `json:"credential_type"`
			} `json:"summary_fields"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		out = append(out, Credential{ID: r.ID, Name: r.Name, Kind: cmp.Or(r.Summary.Type.Kind, r.Kind), Type: r.Summary.Type.Name})
		return nil
	})
	return out, err
}

func (c *HTTPClient) GetInventoryUpdate(ctx context.Context, id int) (Job, error) {
	var j jobJSON
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/inventory_updates/%d/", id), nil, nil, &j); err != nil {
		return Job{}, err
	}
	job := c.toJob(j, false)
	u := *c.base
	u.Fragment = fmt.Sprintf("/jobs/inventory/%d/output", id)
	job.URL = u.String()
	return job, nil
}

type templateJSON struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	AskLimit      bool      `json:"ask_limit_on_launch"`
	AskVariables  bool      `json:"ask_variables_on_launch"`
	AskInventory  bool      `json:"ask_inventory_on_launch"`
	AskCredential bool      `json:"ask_credential_on_launch"`
	SurveyEnabled bool      `json:"survey_enabled"`
	Modified      time.Time `json:"modified"`
	Summary       struct {
		Organization struct {
			Name string `json:"name"`
		} `json:"organization"`
		Labels struct {
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		} `json:"labels"`
	} `json:"summary_fields"`
}

func (c *HTTPClient) Templates(ctx context.Context, f TemplateFilter) ([]Template, error) {
	var out []Template
	for _, workflow := range []bool{false, true} {
		tpl, _ := kindPath(workflow)
		q := url.Values{"page_size": {"200"}, "order_by": {"name"}}
		if f.Organization != "" {
			q.Set("organization__name", f.Organization)
		}
		if len(f.Labels) > 0 && !workflow { // AWX can't filter workflow templates on labels
			q.Set("labels__name__in", strings.Join(f.Labels, ","))
		}
		seen := map[int]bool{}
		for page := 1; page < 100; page++ {
			q.Set("page", fmt.Sprint(page))
			var res struct {
				Next    *string        `json:"next"`
				Results []templateJSON `json:"results"`
			}
			if err := c.do(ctx, http.MethodGet, tpl+"/", q, nil, &res); err != nil {
				return nil, err
			}
			for _, t := range res.Results {
				if seen[t.ID] { // a template with two of the labels is listed twice
					continue
				}
				seen[t.ID] = true
				labels := []string{}
				for _, l := range t.Summary.Labels.Results {
					labels = append(labels, l.Name)
				}
				if len(f.Labels) > 0 && !slices.ContainsFunc(labels, func(l string) bool { return slices.Contains(f.Labels, l) }) {
					continue
				}
				out = append(out, Template{ID: t.ID, Name: t.Name, Description: t.Description, Workflow: workflow,
					Organization: t.Summary.Organization.Name, Labels: labels, AskLimit: t.AskLimit,
					AskVariables: t.AskVariables, AskInventory: t.AskInventory, AskCredential: t.AskCredential,
					SurveyEnabled: t.SurveyEnabled, Modified: t.Modified})
			}
			if res.Next == nil {
				break
			}
		}
	}
	return out, nil
}

func (c *HTTPClient) Survey(ctx context.Context, templateID int, workflow bool) ([]SurveyQuestion, error) {
	tpl, _ := kindPath(workflow)
	var spec struct {
		Spec []SurveyQuestion `json:"spec"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/%d/survey_spec/", tpl, templateID), nil, nil, &spec); err != nil {
		return nil, err
	}
	return spec.Spec, nil
}

func (c *HTTPClient) WorkflowNodes(ctx context.Context, workflowJobID int) ([]WorkflowNode, error) {
	var res struct {
		Results []struct {
			ID       int  `json:"id"`
			Job      *int `json:"job"`
			DoNotRun bool `json:"do_not_run"`
			Summary  struct {
				Template struct {
					Name string `json:"name"`
				} `json:"unified_job_template"`
				Job struct {
					Status string `json:"status"`
					Type   string `json:"type"`
				} `json:"job"`
			} `json:"summary_fields"`
		} `json:"results"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/workflow_jobs/%d/workflow_nodes/", workflowJobID),
		url.Values{"page_size": {"200"}, "order_by": {"id"}}, nil, &res); err != nil {
		return nil, err
	}
	out := make([]WorkflowNode, len(res.Results))
	for i, n := range res.Results {
		out[i] = WorkflowNode{ID: n.ID, Name: n.Summary.Template.Name, Status: n.Summary.Job.Status, Kind: n.Summary.Job.Type,
			DoNotRun: n.DoNotRun}
		if n.Job != nil {
			out[i].JobID = *n.Job
		}
	}
	return out, nil
}
