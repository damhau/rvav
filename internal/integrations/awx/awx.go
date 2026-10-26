// Package awx launches AWX job templates and follows their events. A fake
// that plays realistic jobs is used for local development and tests
// (RVAV_FAKE_AWX=1).
package awx

import (
	"context"
	"strings"
)

type LaunchRequest struct {
	Limit     string
	ExtraVars map[string]any
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

type Client interface {
	// LookupJobTemplate returns the ID of a job template (or workflow job
	// template) by name.
	LookupJobTemplate(ctx context.Context, name string, workflow bool) (int, error)
	Launch(ctx context.Context, templateID int, workflow bool, req LaunchRequest) (Job, error)
	GetJob(ctx context.Context, id int, workflow bool) (Job, error)
	// Events returns events with a counter greater than after.
	Events(ctx context.Context, jobID int, after int) ([]Event, error)
	Cancel(ctx context.Context, jobID int, workflow bool) error
	// UpdateInventorySource starts a refresh and returns the update job ID.
	UpdateInventorySource(ctx context.Context, sourceID int) (int, error)
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
