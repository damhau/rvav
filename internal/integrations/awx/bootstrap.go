package awx

import (
	"context"
	"encoding/json"
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
// workflows, and rvav's inventory. Bootstrap sets it up and only ever
// touches that organisation (and rvav's credential type: AWX's credential
// types belong to no organisation).
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
	// RvavInventory is rvav's VMs, read by the rvav.platform inventory
	// plugin with the plugin configuration at SourcePath in the project, and
	// a credential of type CredentialType.
	RvavInventory struct {
		Name           string `yaml:"name"`
		SourcePath     string `yaml:"source_path"`
		CredentialType string `yaml:"credential_type"`
	} `yaml:"rvav_inventory"`
}

// Rvav is how AWX reaches rvav for its inventory: rvav's URL as AWX sees
// it, and an API token of a user with the auditor role.
type Rvav struct {
	URL, Token string
}

type TemplateContent struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	Playbook    string           `yaml:"playbook"`
	Labels      []string         `yaml:"labels"`
	Survey      []SurveyQuestion `yaml:"survey"`
	// AskInventory: the template runs on rvav's VMs, so rvav launches it
	// against its inventory.
	AskInventory bool `yaml:"ask_inventory"`
}

type WorkflowContent struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Labels       []string `yaml:"labels"`
	Nodes        []string `yaml:"nodes"`
	AskInventory bool     `yaml:"ask_inventory"`
}

// SurveyQuestion is a question of an AWX survey, as in survey_spec.
type SurveyQuestion struct {
	Variable    string  `yaml:"variable" json:"variable"`
	Question    string  `yaml:"question" json:"question_name"`
	Description string  `yaml:"description" json:"question_description"`
	Type        string  `yaml:"type" json:"type"`
	Choices     Choices `yaml:"choices" json:"choices,omitempty"`
	Default     any     `yaml:"default" json:"default"`
	Required    bool    `yaml:"required" json:"required"`
	Min         *int    `yaml:"min" json:"min,omitempty"`
	Max         *int    `yaml:"max" json:"max,omitempty"`
}

// Choices are a question's options. AWX gives them as a list, or (older
// surveys) as one string with a line per option.
type Choices []string

func (c *Choices) UnmarshalJSON(b []byte) error {
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*c = list
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*c = nil
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			*c = append(*c, line)
		}
	}
	return nil
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
	return c.ensureAt(ctx, path, path, name, orgID, body)
}

// ensureAt is ensure for objects listed and created at one path and
// updated at another (hosts: /inventories/{id}/hosts/ and /hosts/{id}/).
func (c *HTTPClient) ensureAt(ctx context.Context, path, itemPath, name string, orgID int, body map[string]any) (int, error) {
	id, err := c.find(ctx, path, name, orgID)
	if err != nil {
		return 0, err
	}
	body["name"] = name
	if orgID != 0 {
		body["organization"] = orgID
	}
	if id != 0 {
		return id, c.do(ctx, http.MethodPatch, fmt.Sprintf("%s%d/", itemPath, id), nil, body, nil)
	}
	var created named
	if err := c.do(ctx, http.MethodPost, path, nil, body, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// Bootstrap sets up the content: it updates what exists and adds what is
// missing, then syncs the project. With rvav's URL and a token, rvav's
// inventory gets its source, which is synced once; its ID is returned (0
// without).
func Bootstrap(ctx context.Context, c *HTTPClient, content Content, rvav Rvav, log *slog.Logger) (int, error) {
	org, err := c.ensure(ctx, "/organizations/", content.Organization, 0, map[string]any{
		"description": "rvav's test content (deploy/awx/content.yml)"})
	if err != nil {
		return 0, fmt.Errorf("organization: %w", err)
	}
	project, err := c.ensure(ctx, "/projects/", content.Project.Name, org, map[string]any{
		"scm_type": "git", "scm_url": content.Project.SCMURL, "scm_branch": content.Project.SCMBranch,
		"scm_update_on_launch": false})
	if err != nil {
		return 0, fmt.Errorf("project: %w", err)
	}
	if err := c.syncProject(ctx, project); err != nil {
		return 0, err
	}
	log.Info("AWX project synced", "project", content.Project.Name)

	inventory, err := c.ensure(ctx, "/inventories/", content.Inventory.Name, org, map[string]any{})
	if err != nil {
		return 0, fmt.Errorf("inventory: %w", err)
	}
	for _, h := range content.Inventory.Hosts {
		if _, err := c.ensureAt(ctx, fmt.Sprintf("/inventories/%d/hosts/", inventory), "/hosts/", h, 0, map[string]any{
			"variables": "ansible_connection: local\nansible_python_interpreter: '{{ ansible_playbook_python }}'"}); err != nil {
			return 0, fmt.Errorf("host %s: %w", h, err)
		}
	}

	templates := map[string]int{}
	for _, t := range content.JobTemplates {
		id, err := c.ensure(ctx, "/job_templates/", t.Name, org, map[string]any{
			"description": t.Description, "job_type": "run", "inventory": inventory, "project": project,
			"playbook": t.Playbook, "ask_limit_on_launch": true, "ask_variables_on_launch": true,
			"ask_inventory_on_launch": t.AskInventory, "survey_enabled": len(t.Survey) > 0})
		if err != nil {
			return 0, fmt.Errorf("job template %s: %w", t.Name, err)
		}
		templates[t.Name] = id
		if len(t.Survey) > 0 {
			for i := range t.Survey {
				if t.Survey[i].Default == nil {
					t.Survey[i].Default = "" // AWX's "no default"
				}
			}
			spec := map[string]any{"name": t.Name, "description": t.Description, "spec": t.Survey}
			if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/job_templates/%d/survey_spec/", id), nil, spec, nil); err != nil {
				return 0, fmt.Errorf("survey of %s: %w", t.Name, err)
			}
		}
		if err := c.label(ctx, fmt.Sprintf("/job_templates/%d/labels/", id), org, t.Labels); err != nil {
			return 0, fmt.Errorf("labels of %s: %w", t.Name, err)
		}
	}

	for _, w := range content.Workflows {
		id, err := c.ensure(ctx, "/workflow_job_templates/", w.Name, org, map[string]any{
			"description": w.Description, "ask_limit_on_launch": true, "ask_variables_on_launch": true,
			"ask_inventory_on_launch": w.AskInventory})
		if err != nil {
			return 0, fmt.Errorf("workflow %s: %w", w.Name, err)
		}
		if err := c.workflowNodes(ctx, id, w.Nodes, templates); err != nil {
			return 0, fmt.Errorf("workflow %s: %w", w.Name, err)
		}
		if err := c.label(ctx, fmt.Sprintf("/workflow_job_templates/%d/labels/", id), org, w.Labels); err != nil {
			return 0, fmt.Errorf("labels of %s: %w", w.Name, err)
		}
	}
	log.Info("AWX test content set up", "organization", content.Organization,
		"job_templates", len(content.JobTemplates), "workflows", len(content.Workflows))
	if content.RvavInventory.Name == "" {
		return 0, nil
	}
	return c.rvavInventory(ctx, content, org, project, rvav, log)
}

// rvavInventory sets up rvav's inventory: the credential type that gives
// the inventory plugin RVAV_URL and RVAV_TOKEN, the inventory, and, with
// rvav's URL and a token, a credential and the inventory's source, synced
// once to show whether AWX reaches rvav.
func (c *HTTPClient) rvavInventory(ctx context.Context, content Content, org, project int, rvav Rvav, log *slog.Logger) (int, error) {
	ri := content.RvavInventory
	ct, err := c.find(ctx, "/credential_types/", ri.CredentialType, 0)
	if err != nil {
		return 0, fmt.Errorf("credential type: %w", err)
	}
	if ct == 0 { // created once: AWX refuses changes to a credential type in use
		var created named
		if err := c.do(ctx, http.MethodPost, "/credential_types/", nil, map[string]any{"name": ri.CredentialType, "kind": "cloud",
			"description": "rvav's URL and an API token, for the rvav.platform inventory plugin",
			"inputs": map[string]any{"fields": []map[string]any{
				{"id": "url", "label": "rvav URL", "type": "string"},
				{"id": "token", "label": "API token", "type": "string", "secret": true}},
				"required": []string{"url", "token"}},
			"injectors": map[string]any{"env": map[string]string{"RVAV_URL": "{{ url }}", "RVAV_TOKEN": "{{ token }}"}}}, &created); err != nil {
			return 0, fmt.Errorf("credential type: %w", err)
		}
		ct = created.ID
	}
	inventory, err := c.ensure(ctx, "/inventories/", ri.Name, org, map[string]any{
		"description": "rvav's VMs, from rvav (rvav.platform.rvav)"})
	if err != nil {
		return 0, fmt.Errorf("rvav inventory: %w", err)
	}
	if rvav.URL == "" || rvav.Token == "" {
		log.Info("no rvav URL and token: the rvav inventory has no source yet", "inventory", ri.Name)
		return 0, nil
	}
	cred, err := c.ensure(ctx, "/credentials/", ri.Name+" API", org, map[string]any{"credential_type": ct,
		"description": "rvav's API for the rvav inventory", "inputs": map[string]string{"url": rvav.URL, "token": rvav.Token}})
	if err != nil {
		return 0, fmt.Errorf("rvav credential: %w", err)
	}
	source, err := c.ensureAt(ctx, fmt.Sprintf("/inventories/%d/inventory_sources/", inventory), "/inventory_sources/", ri.Name, 0,
		map[string]any{"source": "scm", "source_project": project, "source_path": ri.SourcePath, "credential": cred,
			"overwrite": true, "overwrite_vars": true, "update_on_launch": false})
	if err != nil {
		return 0, fmt.Errorf("rvav inventory source: %w", err)
	}
	id, err := c.UpdateInventorySource(ctx, source)
	if err == nil {
		err = c.waitUpdate(ctx, "/inventory_updates/", id)
	}
	if err != nil {
		log.Warn("the rvav inventory could not be synced; check that AWX reaches rvav at its URL", "url", rvav.URL, "err", err)
	} else {
		log.Info("rvav inventory synced", "inventory", ri.Name)
	}
	log.Info("set RVAV_AWX_INVENTORY_SOURCE_ID for rvav's worker", "RVAV_AWX_INVENTORY_SOURCE_ID", source)
	return source, nil
}

func (c *HTTPClient) syncProject(ctx context.Context, project int) error {
	var started struct {
		ID            int `json:"id"`
		ProjectUpdate int `json:"project_update"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/update/", project), nil, map[string]any{}, &started); err != nil {
		return fmt.Errorf("project sync: %w", err)
	}
	if err := c.waitUpdate(ctx, "/project_updates/", max(started.ProjectUpdate, started.ID)); err != nil {
		return fmt.Errorf("project sync: %w", err)
	}
	return nil
}

// waitUpdate waits for a project or inventory update to finish, five
// minutes at most.
func (c *HTTPClient) waitUpdate(ctx context.Context, path string, id int) error {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		var u struct {
			Status          string `json:"status"`
			ResultTraceback string `json:"result_traceback"`
			Explanation     string `json:"job_explanation"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s%d/", path, id), nil, nil, &u); err != nil {
			return err
		}
		switch u.Status {
		case "successful":
			return nil
		case "failed", "error", "canceled":
			return fmt.Errorf("update %d %s %s", id, u.Status, strings.TrimSpace(u.Explanation+" "+u.ResultTraceback))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("update %d did not finish in 5 minutes", id)
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
