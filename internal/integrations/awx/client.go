package awx

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient talks to the AWX REST API (/api/v2) with an OAuth2 token.
type HTTPClient struct {
	base  *url.URL
	token string
	http  *http.Client
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
	return &HTTPClient{base: u, token: token, http: &http.Client{Transport: tr, Timeout: 30 * time.Second}}, nil
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
		return fmt.Errorf("awx %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
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
	if err := c.do(ctx, http.MethodGet, tpl+"/", url.Values{"name": {name}}, nil, &res); err != nil {
		return 0, err
	}
	if len(res.Results) == 0 {
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

// Launch starts a job. The template must allow "Prompt on launch" for limit
// and variables, or AWX silently ignores them.
func (c *HTTPClient) Launch(ctx context.Context, templateID int, workflow bool, req LaunchRequest) (Job, error) {
	tpl, _ := kindPath(workflow)
	body := map[string]any{"extra_vars": req.ExtraVars}
	if req.Limit != "" {
		body["limit"] = req.Limit
	}
	var j jobJSON
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/launch/", tpl, templateID), nil, body, &j); err != nil {
		return Job{}, err
	}
	return c.toJob(j, workflow), nil
}

func (c *HTTPClient) GetJob(ctx context.Context, id int, workflow bool) (Job, error) {
	_, jobs := kindPath(workflow)
	var j jobJSON
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/%d/", jobs, id), nil, nil, &j); err != nil {
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
