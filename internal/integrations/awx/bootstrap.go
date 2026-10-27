package awx

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Content is test content for an AWX (deploy/awx/content.yml): an
// organisation, a project, an inventory, job templates with surveys and
// workflows. Bootstrap sets it up and only ever touches that organisation.
type Content struct {
	Organization string `yaml:"organization"`
	Project      struct {
		Name      string `yaml:"name"`
		SCMURL    string `yaml:"scm_url"`
		SCMBranch string `yaml:"scm_branch"`
	} `yaml:"project"`
	Inventory struct {
		Name  string   `yaml:"name"`
		Hosts []string `yaml:"hosts"`
	} `yaml:"inventory"`
	JobTemplates []TemplateContent `yaml:"job_templates"`
	Workflows    []WorkflowContent `yaml:"workflow_job_templates"`
}

type TemplateContent struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	Playbook    string           `yaml:"playbook"`
	Labels      []string         `yaml:"labels"`
	Survey      []SurveyQuestion `yaml:"survey"`
}

type WorkflowContent struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Labels      []string `yaml:"labels"`
	Nodes       []string `yaml:"nodes"`
}

// SurveyQuestion is a question of an AWX survey, as in survey_spec.
type SurveyQuestion struct {
	Variable    string   `yaml:"variable" json:"variable"`
	Question    string   `yaml:"question" json:"question_name"`
	Description string   `yaml:"description" json:"question_description"`
	Type        string   `yaml:"type" json:"type"`
	Choices     []string `yaml:"choices" json:"choices,omitempty"`
	Default     any      `yaml:"default" json:"default"`
	Required    bool     `yaml:"required" json:"required"`
	Min         *int     `yaml:"min" json:"min,omitempty"`
	Max         *int     `yaml:"max" json:"max,omitempty"`
}

func LoadContent(path string) (Content, error) {
	var c Content
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.Organization == "" {
		return c, fmt.Errorf("%s: no organization", path)
	}
	return c, nil
}

type named struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// find returns the ID of the object of this name at path (0 if none),
// within the organisation when orgID is set.
func (c *HTTPClient) find(ctx context.Context, path, name string, orgID int) (int, error) {
	q := url.Values{"name": {name}}
	if orgID != 0 {
		q.Set("organization", fmt.Sprint(orgID))
	}
	var res struct{ Results []named }
	if err := c.do(ctx, http.MethodGet, path, q, nil, &res); err != nil {
		return 0, err
	}
	if len(res.Results) == 0 {
		return 0, nil
	}
	return res.Results[0].ID, nil
}

// ensure creates the named object at path, or updates it with body.
func (c *HTTPClient) ensure(ctx context.Context, path, name string, orgID int, body map[string]any) (int, error) {
	id, err := c.find(ctx, path, name, orgID)
	if err != nil {
		return 0, err
	}
	body["name"] = name
	if orgID != 0 {
		body["organization"] = orgID
	}
	if id != 0 {
		return id, c.do(ctx, http.MethodPatch, fmt.Sprintf("%s%d/", path, id), nil, body, nil)
	}
	var created named
	if err := c.do(ctx, http.MethodPost, path, nil, body, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// Bootstrap sets up the content: it updates what exists and adds what is
// missing, then syncs the project.
func Bootstrap(ctx context.Context, c *HTTPClient, content Content, log *slog.Logger) error {
	org, err := c.ensure(ctx, "/organizations/", content.Organization, 0, map[string]any{
		"description": "rvav's test content (deploy/awx/content.yml)"})
	if err != nil {
		return fmt.Errorf("organization: %w", err)
	}
	project, err := c.ensure(ctx, "/projects/", content.Project.Name, org, map[string]any{
		"scm_type": "git", "scm_url": content.Project.SCMURL, "scm_branch": content.Project.SCMBranch,
		"scm_update_on_launch": false})
	if err != nil {
		return fmt.Errorf("project: %w", err)
	}
	if err := c.syncProject(ctx, project); err != nil {
		return err
	}
	log.Info("AWX project synced", "project", content.Project.Name)

	inventory, err := c.ensure(ctx, "/inventories/", content.Inventory.Name, org, map[string]any{})
	if err != nil {
		return fmt.Errorf("inventory: %w", err)
	}
	for _, h := range content.Inventory.Hosts {
		if _, err := c.ensure(ctx, fmt.Sprintf("/inventories/%d/hosts/", inventory), h, 0, map[string]any{
			"variables": "ansible_connection: local\nansible_python_interpreter: '{{ ansible_playbook_python }}'"}); err != nil {
			return fmt.Errorf("host %s: %w", h, err)
		}
	}

	templates := map[string]int{}
	for _, t := range content.JobTemplates {
		id, err := c.ensure(ctx, "/job_templates/", t.Name, org, map[string]any{
			"description": t.Description, "job_type": "run", "inventory": inventory, "project": project,
			"playbook": t.Playbook, "ask_limit_on_launch": true, "ask_variables_on_launch": true,
			"survey_enabled": len(t.Survey) > 0})
		if err != nil {
			return fmt.Errorf("job template %s: %w", t.Name, err)
		}
		templates[t.Name] = id
		if len(t.Survey) > 0 {
			spec := map[string]any{"name": t.Name, "description": t.Description, "spec": t.Survey}
			if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/job_templates/%d/survey_spec/", id), nil, spec, nil); err != nil {
				return fmt.Errorf("survey of %s: %w", t.Name, err)
			}
		}
		if err := c.label(ctx, fmt.Sprintf("/job_templates/%d/labels/", id), org, t.Labels); err != nil {
			return fmt.Errorf("labels of %s: %w", t.Name, err)
		}
	}

	for _, w := range content.Workflows {
		id, err := c.ensure(ctx, "/workflow_job_templates/", w.Name, org, map[string]any{
			"description": w.Description, "ask_limit_on_launch": true, "ask_variables_on_launch": true})
		if err != nil {
			return fmt.Errorf("workflow %s: %w", w.Name, err)
		}
		if err := c.workflowNodes(ctx, id, w.Nodes, templates); err != nil {
			return fmt.Errorf("workflow %s: %w", w.Name, err)
		}
		if err := c.label(ctx, fmt.Sprintf("/workflow_job_templates/%d/labels/", id), org, w.Labels); err != nil {
			return fmt.Errorf("labels of %s: %w", w.Name, err)
		}
	}
	log.Info("AWX test content set up", "organization", content.Organization,
		"job_templates", len(content.JobTemplates), "workflows", len(content.Workflows))
	return nil
}

func (c *HTTPClient) syncProject(ctx context.Context, project int) error {
	var started struct {
		ID            int `json:"id"`
		ProjectUpdate int `json:"project_update"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/update/", project), nil, map[string]any{}, &started); err != nil {
		return fmt.Errorf("project sync: %w", err)
	}
	id := max(started.ProjectUpdate, started.ID)
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		var u struct {
			Status          string `json:"status"`
			ResultTraceback string `json:"result_traceback"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/project_updates/%d/", id), nil, nil, &u); err != nil {
			return err
		}
		switch u.Status {
		case "successful":
			return nil
		case "failed", "error", "canceled":
			return fmt.Errorf("project sync %d %s %s", id, u.Status, strings.TrimSpace(u.ResultTraceback))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("project sync %d did not finish in 5 minutes", id)
}

// label attaches labels (made in the organisation when missing).
func (c *HTTPClient) label(ctx context.Context, path string, org int, labels []string) error {
	for _, l := range labels {
		id, err := c.ensure(ctx, "/labels/", l, org, map[string]any{})
		if err != nil {
			return err
		}
		if err := c.do(ctx, http.MethodPost, path, nil, map[string]any{"id": id}, nil); err != nil {
			return err
		}
	}
	return nil
}

// workflowNodes replaces the workflow's nodes with a chain of templates,
// each run when the one before succeeds.
func (c *HTTPClient) workflowNodes(ctx context.Context, workflow int, nodes []string, templates map[string]int) error {
	var existing struct{ Results []named }
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/workflow_job_templates/%d/workflow_nodes/", workflow), nil, nil, &existing); err != nil {
		return err
	}
	for _, n := range existing.Results {
		if err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/workflow_job_template_nodes/%d/", n.ID), nil, nil, nil); err != nil {
			return err
		}
	}
	prev := 0
	for _, name := range nodes {
		tpl, ok := templates[name]
		if !ok {
			return fmt.Errorf("node %s is not one of the job templates", name)
		}
		var node named
		if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/workflow_job_templates/%d/workflow_nodes/", workflow), nil,
			map[string]any{"unified_job_template": tpl}, &node); err != nil {
			return err
		}
		if prev != 0 {
			if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/workflow_job_template_nodes/%d/success_nodes/", prev), nil,
				map[string]any{"id": node.ID}, nil); err != nil {
				return err
			}
		}
		prev = node.ID
	}
	return nil
}
