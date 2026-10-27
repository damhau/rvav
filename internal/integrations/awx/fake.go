package awx

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fake plays AWX jobs in memory. Each task takes TaskDelay; a task whose
// title equals FailTask fails the job, as the IdM join does in the mockup.
type Fake struct {
	TaskDelay time.Duration
	FailTask  string
	// Plays maps job template names to their task titles.
	Plays map[string][]string
	// Surveys are the templates' surveys (the others have none), and
	// Workflows the templates that are workflow job templates.
	Surveys   map[string][]SurveyQuestion
	Workflows map[string]bool
	// Labels are the templates' labels besides "rvav", which they all have.
	Labels map[string][]string

	mu      sync.Mutex
	nextJob int
	jobs    map[int]*fakeJob
	now     func() time.Time
}

type fakeJob struct {
	id        int
	template  string
	limit     string
	inventory int
	extraVars map[string]any
	started   time.Time
	canceled  bool
	tasks     []string
	update    bool // an inventory update rather than a playbook run
}

// DefaultTemplates are the job templates used by the seed catalog.
func DefaultTemplates() map[string][]string {
	return map[string][]string{
		"linux-baseline": {"Gathering Facts", "baseline : configure chrony", "baseline : harden sshd",
			"baseline : set journald retention", "baseline : register with Satellite"},
		"idm-join":         {"Gathering Facts", "idm : install ipa-client", "idm : join realm", "idm : apply HBAC rules"},
		"monitoring-agent": {"Gathering Facts", "monitoring : install node_exporter", "monitoring : open port 9100", "monitoring : register target"},
		"role-nginx":       {"Gathering Facts", "nginx : install packages", "nginx : deploy TLS defaults", "nginx : start service"},
		"win-baseline":     {"Gathering Facts", "win_baseline : apply CIS GPO", "win_baseline : configure WSUS", "win_baseline : join domain"},
		"pg-create-role":   {"Gathering Facts", "postgres : create role", "vault : store password"},
		"ddi-dns-record":   {"Gathering Facts", "efficientip : add record"},
		"pki-renew-deploy": {"Gathering Facts", "pki : issue certificate", "pki : deploy certificate", "pki : reload services"},
		"idm-hbac-group":   {"Gathering Facts", "idm : create group", "idm : add sudo rule", "idm : add HBAC rule"},
		// A workflow: each entry is a node.
		"fw-flow-request": {"fw-check-flow", "fw-open-change", "fw-push-rules"},
	}
}

func NewFake(taskDelay time.Duration, failTask string) *Fake {
	return &Fake{TaskDelay: taskDelay, FailTask: failTask, Plays: DefaultTemplates(),
		Surveys: DefaultSurveys(), Workflows: map[string]bool{"fw-flow-request": true},
		Labels:  map[string][]string{"monitoring-agent": {"day2"}},
		nextJob: 18240, jobs: map[int]*fakeJob{}, now: time.Now}
}

func (f *Fake) templateNames() []string {
	names := make([]string, 0, len(f.Plays))
	for n := range f.Plays {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (f *Fake) LookupJobTemplate(_ context.Context, name string, _ bool) (int, error) {
	for i, n := range f.templateNames() {
		if n == name {
			return 100 + i, nil
		}
	}
	return 0, fmt.Errorf("awx job template %q not found", name)
}

func (f *Fake) Launch(_ context.Context, templateID int, _ bool, req LaunchRequest) (Job, error) {
	names := f.templateNames()
	idx := templateID - 100
	if idx < 0 || idx >= len(names) {
		return Job{}, fmt.Errorf("awx job template %d not found", templateID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextJob++
	j := &fakeJob{id: f.nextJob, template: names[idx], limit: req.Limit, inventory: req.Inventory, extraVars: req.ExtraVars, started: f.now(), tasks: f.Plays[names[idx]]}
	f.jobs[j.id] = j
	return f.jobLocked(j), nil
}

// progress returns how many tasks have finished and whether the job failed.
func (f *Fake) progress(j *fakeJob) (done int, failedAt int) {
	failedAt = -1
	elapsed := f.now().Sub(j.started)
	done = int(elapsed / f.TaskDelay)
	if done > len(j.tasks) {
		done = len(j.tasks)
	}
	for i := 0; i < done; i++ {
		if f.FailTask != "" && j.tasks[i] == f.FailTask {
			return i + 1, i
		}
	}
	return done, failedAt
}

func (f *Fake) jobLocked(j *fakeJob) Job {
	done, failedAt := f.progress(j)
	job := Job{ID: j.id, Status: "running", URL: fmt.Sprintf("https://awx.corp.example/#/jobs/playbook/%d/output", j.id)}
	switch {
	case j.canceled:
		job.Status, job.Finished = "canceled", true
	case failedAt >= 0:
		job.Status, job.Failed, job.Finished = "failed", true, true
	case done >= len(j.tasks):
		job.Status, job.Finished = "successful", true
	}
	return job
}

func (f *Fake) GetJob(_ context.Context, id int, _ bool) (Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("awx job %d not found", id)
	}
	return f.jobLocked(j), nil
}

// Events replays the job's events up to the current point in time.
func (f *Fake) Events(_ context.Context, id int, after int) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, fmt.Errorf("awx job %d not found", id)
	}
	host := j.limit
	if host == "" {
		host = "localhost"
	}
	done, failedAt := f.progress(j)
	var all []Event
	add := func(e Event) {
		e.Counter = len(all) + 1
		all = append(all, e)
	}
	add(Event{Event: "playbook_on_play_start", Stdout: fmt.Sprintf("PLAY [%s] %s", playTitle(j.template), stars(j.template))})
	for i, task := range j.tasks {
		if i > done || (i == done && j.canceled) {
			break
		}
		add(Event{Event: "playbook_on_task_start", Task: task, Stdout: "\r\nTASK [" + task + "] " + stars(task)})
		if i == done {
			break // running
		}
		switch {
		case i == failedAt:
			msg := "ipa-client-install failed: Unable to discover domain, not provisioning DNS records for idm.corp.example"
			if !strings.HasPrefix(task, "idm") {
				msg = "task failed (simulated by RVAV_FAKE_AWX_FAIL_TASK)"
			}
			add(Event{Event: "runner_on_failed", Task: task, Host: host, Failed: true, Message: msg,
				Stdout: fmt.Sprintf(`fatal: [%s]: FAILED! => {"changed": false, "msg": %q}`, host, msg)})
		case task == "Gathering Facts":
			add(Event{Event: "runner_on_ok", Task: task, Host: host, Stdout: "ok: [" + host + "]"})
		default:
			add(Event{Event: "runner_on_ok", Task: task, Host: host, Changed: true, Stdout: "changed: [" + host + "]"})
		}
		if i == failedAt {
			break
		}
	}
	job := f.jobLocked(j)
	if job.Finished && !j.canceled {
		ok, changed, failed := done, done-1, 0
		if failedAt >= 0 {
			ok, changed, failed = failedAt, failedAt-1, 1
		}
		if changed < 0 {
			changed = 0
		}
		add(Event{Event: "playbook_on_stats", Stdout: fmt.Sprintf("\r\nPLAY RECAP %s\r\n%-26s : ok=%d    changed=%d    unreachable=0    failed=%d    skipped=0",
			strings.Repeat("*", 64), host, ok, changed, failed)})
	}
	var out []Event
	for _, e := range all {
		if e.Counter > after {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *Fake) Cancel(_ context.Context, id int, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j, ok := f.jobs[id]; ok {
		j.canceled = true
	}
	return nil
}

func (f *Fake) UpdateInventorySource(_ context.Context, _ int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextJob++
	f.jobs[f.nextJob] = &fakeJob{id: f.nextJob, started: f.now(), update: true, tasks: []string{"update"}}
	return f.nextJob, nil
}

// FakeInventory is the inventory the fake's inventory sources fill.
const FakeInventory = 2

func (f *Fake) InventoryOfSource(context.Context, int) (int, error) { return FakeInventory, nil }

// LaunchedInventory returns the inventory a job was launched against (0:
// the template's).
func (f *Fake) LaunchedInventory(id int) int { return f.Launched(id).Inventory }

// Launched returns what a job was launched with.
func (f *Fake) Launched(id int) LaunchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j, ok := f.jobs[id]; ok {
		return LaunchRequest{Limit: j.limit, Inventory: j.inventory, ExtraVars: j.extraVars}
	}
	return LaunchRequest{}
}

func (f *Fake) GetInventoryUpdate(ctx context.Context, id int) (Job, error) {
	j, err := f.GetJob(ctx, id, false)
	j.URL = fmt.Sprintf("https://awx.corp.example/#/inventories/inventory/1/sources/1/jobs/%d", id)
	return j, err
}

func playTitle(template string) string {
	words := strings.Split(strings.ReplaceAll(template, "-", " "), " ")
	if len(words) > 0 && words[0] != "" {
		words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	}
	return strings.Join(words, " ")
}

func stars(s string) string {
	n := 72 - len(s)
	if n < 4 {
		n = 4
	}
	return strings.Repeat("*", n)
}

// DefaultSurveys are the surveys of the fake's templates.
func DefaultSurveys() map[string][]SurveyQuestion {
	return map[string][]SurveyQuestion{
		"monitoring-agent": {
			{Variable: "monitoring_agent", Question: "Agent", Type: "multiplechoice", Choices: Choices{"node_exporter", "telegraf"},
				Default: "node_exporter", Required: true},
			{Variable: "listen_port", Question: "Port", Type: "integer", Min: ptr(1024), Max: ptr(65535), Default: float64(9100), Required: true},
		},
	}
}

func ptr[T any](v T) *T { return &v }

// Templates lists the fake's job templates, all with the label rvav and
// prompts for limit and variables.
func (f *Fake) Templates(_ context.Context, filter TemplateFilter) ([]Template, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Template
	for i, n := range f.templateNames() {
		labels := append([]string{"rvav"}, f.Labels[n]...)
		if len(filter.Labels) > 0 && !slices.ContainsFunc(labels, func(l string) bool { return slices.Contains(filter.Labels, l) }) {
			continue
		}
		out = append(out, Template{ID: 100 + i, Name: n, Workflow: f.Workflows[n], Organization: "rvav", Labels: labels,
			AskLimit: true, AskVariables: true, AskInventory: true, SurveyEnabled: len(f.Surveys[n]) > 0})
	}
	return out, nil
}

func (f *Fake) Survey(_ context.Context, templateID int, _ bool) ([]SurveyQuestion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := f.templateNames()
	if idx := templateID - 100; idx >= 0 && idx < len(names) {
		return f.Surveys[names[idx]], nil
	}
	return nil, fmt.Errorf("awx job template %d not found", templateID)
}

// WorkflowNodes plays a workflow as one node per task of its template.
func (f *Fake) WorkflowNodes(_ context.Context, id int) ([]WorkflowNode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, fmt.Errorf("awx workflow job %d not found", id)
	}
	done, failedAt := f.progress(j)
	out := make([]WorkflowNode, len(j.tasks))
	for i, t := range j.tasks {
		out[i] = WorkflowNode{ID: id*100 + i, Name: t}
		switch {
		case failedAt >= 0 && i > failedAt:
			out[i].DoNotRun = true
		case i == failedAt:
			out[i].Status = "failed"
		case i < done:
			out[i].Status = "successful"
		case i == done && !j.canceled:
			out[i].Status = "running"
		}
	}
	return out, nil
}
