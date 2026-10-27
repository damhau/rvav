// Package awx launches AWX job templates and follows their events. A fake
// that plays realistic jobs is used for local development and tests
// (RVAV_FAKE_AWX=1).
package awx

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrNoInventoryPrompt is a template asked to run against another
// inventory that doesn't prompt for one on launch (AWX would ignore it).
var ErrNoInventoryPrompt = errors.New("the template does not prompt for an inventory on launch")

type LaunchRequest struct {
	Limit     string
	ExtraVars map[string]any
	// Inventory, when set, is the inventory the job runs against instead of
	// the template's: rvav's, for jobs on its VMs. The template must prompt
	// for an inventory on launch.
	Inventory int
}

type Job struct {
	ID       int
	Status   string // pending, waiting, running, successful, failed, error, canceled
	Failed   bool
	Finished bool
	URL      string
}

// Event is one AWX job event, in counter order.
type Event struct {
	Counter int
	Event   string // playbook_on_play_start, playbook_on_task_start, runner_on_ok, runner_on_failed, playbook_on_stats, ...
	Task    string
	Host    string
	Stdout  string
	Changed bool
	Failed  bool
	Message string // error message of a failed task
}

// Template is a job template or workflow job template.
type Template struct {
	ID           int
	Name         string
	Description  string
	Workflow     bool
	Organization string
	Labels       []string
	// What the template lets a launch set ("Prompt on launch").
	AskLimit, AskVariables, AskInventory, AskCredential bool
	SurveyEnabled                                       bool
	Modified                                            time.Time
}

// TemplateFilter chooses templates: of an organisation, with any of the
// labels (all templates when empty).
type TemplateFilter struct {
	Organization string
	Labels       []string
}

// WorkflowNode is a node of a workflow job, and the job it ran.
type WorkflowNode struct {
	ID       int
	Name     string // the template's name
	JobID    int    // 0 until the node runs
	Status   string // the job's; "" until it runs
	DoNotRun bool   // skipped because of an earlier node's outcome
}

type Client interface {
	// LookupJobTemplate returns the ID of a job template (or workflow job
	// template) by name, in rvav's organisation when one is set.
	LookupJobTemplate(ctx context.Context, name string, workflow bool) (int, error)
	// Templates lists job templates and workflow job templates.
	Templates(ctx context.Context, f TemplateFilter) ([]Template, error)
	// Survey returns a template's survey questions (none when it has no survey).
	Survey(ctx context.Context, templateID int, workflow bool) ([]SurveyQuestion, error)
	// WorkflowNodes lists a workflow job's nodes.
	WorkflowNodes(ctx context.Context, workflowJobID int) ([]WorkflowNode, error)
	Launch(ctx context.Context, templateID int, workflow bool, req LaunchRequest) (Job, error)
	GetJob(ctx context.Context, id int, workflow bool) (Job, error)
	// Events returns a playbook job's events with a counter greater than after.
	Events(ctx context.Context, jobID int, after int) ([]Event, error)
	Cancel(ctx context.Context, jobID int, workflow bool) error
	// UpdateInventorySource starts a refresh and returns the update job ID.
	UpdateInventorySource(ctx context.Context, sourceID int) (int, error)
	// InventoryOfSource returns the ID of the inventory a source fills.
	InventoryOfSource(ctx context.Context, sourceID int) (int, error)
	GetInventoryUpdate(ctx context.Context, id int) (Job, error)
}

// Done reports whether a job status is final.
func Done(status string) bool {
	switch status {
	case "successful", "failed", "error", "canceled":
		return true
	}
	return false
}

// LineKind classifies an Ansible stdout line for colouring in the UI.
func LineKind(line string) string {
	l := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(l, "PLAY RECAP"):
		return "recap"
	case strings.HasPrefix(l, "PLAY [") || strings.HasPrefix(l, "TASK ["):
		return "header"
	case strings.HasPrefix(l, "ok:"):
		return "ok"
	case strings.HasPrefix(l, "changed:"):
		return "changed"
	case strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "failed:"):
		return "failed"
	case strings.HasPrefix(l, "skipping:"):
		return "skipped"
	case strings.Contains(l, "UNREACHABLE"):
		return "unreachable"
	}
	return "plain"
}
