package awxcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/store"
)

// Sync imports the AWX templates the filter chooses, every interval, as
// catalog items; and those with one of ActionLabels (in the filter's
// organisation) as day-2 actions (ADR-0018).
type Sync struct {
	Store        *store.Store
	AWX          awx.Client
	Filter       awx.TemplateFilter
	ActionLabels []string
	Log          *slog.Logger
	Interval     time.Duration
}

func (s *Sync) Run(ctx context.Context) error {
	for {
		if _, err := s.SyncOnce(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("AWX catalog sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Interval):
		}
	}
}

// Result is what a sync did.
type Result struct {
	Templates, Created, Linked, Gone int
	// Actions are the templates synced as day-2 actions, ActionsGone those
	// no longer chosen.
	Actions, ActionsGone int
}

// SyncOnce reads the templates: a new one becomes an item (or is linked to
// the AWX item already launching it, as the seeded ones are), and every
// one's survey and launch settings are recorded so changes show. Templates
// no longer chosen are flagged gone; their items stay.
func (s *Sync) SyncOnce(ctx context.Context) (res Result, err error) {
	// The sync service and an admin's "Sync now" don't run at once: both
	// would make an item of a new template.
	err = s.Store.WithLock(ctx, store.LockAWXSync, func() error {
		res, err = s.syncOnce(ctx)
		return err
	})
	return res, err
}

func (s *Sync) syncOnce(ctx context.Context) (Result, error) {
	var res Result
	templates, err := s.AWX.Templates(ctx, s.Filter)
	if err != nil {
		return res, err
	}
	known, err := s.Store.AWXTemplates(ctx)
	if err != nil {
		return res, err
	}
	type key struct {
		id       int
		workflow bool
	}
	byKey := map[key]store.AWXTemplate{}
	for _, k := range known {
		byKey[key{k.AWXID, k.Workflow}] = k
	}
	seenIDs, seenWorkflow := []int{}, []bool{}
	for _, t := range templates {
		res.Templates++
		seenIDs, seenWorkflow = append(seenIDs, t.ID), append(seenWorkflow, t.Workflow)
		survey := []awx.SurveyQuestion{}
		if t.SurveyEnabled {
			if survey, err = s.AWX.Survey(ctx, t.ID, t.Workflow); err != nil {
				return res, err
			}
		}
		_, _, _, formWarnings := Form(t.Name, survey)
		warnings := formWarnings
		if !t.AskVariables {
			warnings = append(warnings, "Variables are not prompted on launch: AWX ignores rvav's context and anything outside the survey.")
		}
		if slices.ContainsFunc(t.Labels, func(l string) bool { return slices.Contains(s.ActionLabels, l) }) {
			warnings = append(warnings, "It is a day-2 action too: from the catalog it runs on its own inventory, with no limit.")
		}
		row := store.AWXTemplate{AWXID: t.ID, Workflow: t.Workflow, Name: t.Name, Warnings: warnings}
		if !t.Modified.IsZero() {
			row.ModifiedAt = &t.Modified
		}
		row.Survey, _ = json.Marshal(survey)
		var imported json.RawMessage
		if k, ok := byKey[key{t.ID, t.Workflow}]; !ok || k.ItemID == nil {
			itemID, err := s.Store.FindAWXItem(ctx, t.Name, t.Workflow)
			switch {
			case err == nil:
				res.Linked++
			case errors.Is(err, store.ErrNotFound):
				if itemID, err = s.create(ctx, t, survey); err != nil {
					return res, err
				}
				res.Created++
			default:
				return res, err
			}
			row.ItemID, imported = &itemID, row.Survey
		}
		if err := s.Store.UpsertAWXTemplate(ctx, row, imported); err != nil {
			return res, err
		}
	}
	if res.Gone, err = s.Store.MarkAWXTemplatesGone(ctx, seenIDs, seenWorkflow); err != nil {
		return res, err
	}
	if len(s.ActionLabels) > 0 {
		if err := s.syncActions(ctx, &res); err != nil {
			return res, err
		}
	}
	s.Log.Info("AWX catalog synced", "templates", res.Templates, "created", res.Created, "linked", res.Linked, "gone", res.Gone,
		"actions", res.Actions, "actions_gone", res.ActionsGone)
	return res, nil
}

// syncActions records the templates with an action label as day-2
// actions, with their form from the survey; those no longer chosen are
// flagged gone and no longer offered.
func (s *Sync) syncActions(ctx context.Context, res *Result) error {
	templates, err := s.AWX.Templates(ctx, awx.TemplateFilter{Organization: s.Filter.Organization, Labels: s.ActionLabels})
	if err != nil {
		return err
	}
	known, err := s.Store.AWXActions(ctx)
	if err != nil {
		return err
	}
	type ref struct {
		id       int
		workflow bool
	}
	keys := map[string]ref{}
	for _, a := range known {
		keys[a.Key] = ref{a.AWXID, a.Workflow}
	}
	seenIDs, seenWorkflow := []int{}, []bool{}
	for _, t := range templates {
		res.Actions++
		// Seen even when it can't be synced now: it stays as it was rather
		// than being taken for gone.
		seenIDs, seenWorkflow = append(seenIDs, t.ID), append(seenWorkflow, t.Workflow)
		survey := []awx.SurveyQuestion{}
		if t.SurveyEnabled {
			if survey, err = s.AWX.Survey(ctx, t.ID, t.Workflow); err != nil {
				s.Log.Warn("AWX action not synced", "template", t.Name, "err", err)
				continue
			}
		}
		name := displayName(t.Name)
		schema, ui, initial, formWarnings := Form(name, survey)
		launch := actionWarnings(t)
		// A key of its own: "Install agent" and "install_agent" make the same
		// slug, and a renamed template's old name may come back.
		key := "awx." + slug(t.Name)
		if t.Workflow {
			key += "-workflow"
		}
		if r, taken := keys[key]; taken && r != (ref{t.ID, t.Workflow}) {
			key += "-" + strconv.Itoa(t.ID)
		}
		keys[key] = ref{t.ID, t.Workflow}
		j := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
		// A new action with a launch problem starts turned off.
		if err := s.Store.SyncAWXAction(ctx, store.AWXAction{Key: key, AWXID: t.ID, Workflow: t.Workflow, Template: t.Name,
			Name: name, Description: t.Description, FormSchema: j(schema), UISchema: j(ui), Initial: j(initial),
			Warnings: append(launch, formWarnings...), Enabled: len(launch) == 0}); err != nil {
			s.Log.Warn("AWX action not synced", "template", t.Name, "err", err)
		}
	}
	res.ActionsGone, err = s.Store.MarkAWXActionsGone(ctx, seenIDs, seenWorkflow)
	return err
}

// displayName makes a template's name readable: "install-monitoring-agent"
// is "Install monitoring agent".
func displayName(template string) string {
	name := []rune(strings.ReplaceAll(strings.ReplaceAll(template, "-", " "), "_", " "))
	if len(name) == 0 {
		return template
	}
	return string(unicode.ToUpper(name[0])) + string(name[1:])
}

func slug(s string) string {
	return strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// actionWarnings are the launch settings that keep a day-2 action from
// running as meant: rvav refuses to launch it on a VM without the limit
// and inventory prompted.
func actionWarnings(t awx.Template) []string {
	var out []string
	if !t.AskLimit {
		out = append(out, "Limit is not prompted on launch: rvav won't run it, as it would run on every host.")
	}
	if !t.AskInventory {
		out = append(out, "Inventory is not prompted on launch: rvav won't run it, as it can't run on rvav's inventory.")
	}
	if !t.AskVariables {
		out = append(out, "Variables are not prompted on launch: AWX ignores rvav's context and anything outside the survey.")
	}
	return out
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Sync) create(ctx context.Context, t awx.Template, survey []awx.SurveyQuestion) (string, error) {
	name := displayName(t.Name)
	schema, ui, initial, _ := Form(name, survey)
	typ, icon := "awx_job_template", "terminal"
	if t.Workflow {
		typ, icon = "awx_workflow", "workflow"
	}
	category := "automation"
	for _, l := range t.Labels {
		if c, ok := strings.CutPrefix(l, "category:"); ok {
			category = c
		}
	}
	if ok, err := s.Store.CategoryExists(ctx, category); err != nil {
		return "", err
	} else if !ok {
		category = "automation"
	}
	summary := t.Description
	if summary == "" {
		summary = "Runs the AWX template " + t.Name + "."
	}
	bp := map[string]any{"kind": "awx", "awx": map[string]any{"title": name, "job_template": t.Name, "template_id": t.ID, "workflow": t.Workflow}}
	j := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	it := store.NewAWXItem{ID: ids.New(ids.Item), VersionID: ids.New(ids.Version),
		Slug: slug(t.Name), Name: name, Summary: summary,
		Category: category, Type: typ, Icon: icon, AWXID: t.ID, AWXName: t.Name,
		Schema: j(schema), UI: j(ui), Initial: j(initial), Blueprint: j(bp)}
	return it.ID, s.Store.CreateAWXItem(ctx, it)
}
