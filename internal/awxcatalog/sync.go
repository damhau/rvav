package awxcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/store"
)

// Sync imports the AWX templates the filter chooses, every interval.
type Sync struct {
	Store    *store.Store
	AWX      awx.Client
	Filter   awx.TemplateFilter
	Log      *slog.Logger
	Interval time.Duration
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
}

// SyncOnce reads the templates: a new one becomes an item (or is linked to
// the AWX item already launching it, as the seeded ones are), and every
// one's survey and launch settings are recorded so changes show. Templates
// no longer chosen are flagged gone; their items stay.
func (s *Sync) SyncOnce(ctx context.Context) (Result, error) {
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
		row := store.AWXTemplate{AWXID: t.ID, Workflow: t.Workflow, Name: t.Name, Warnings: append(launchWarnings(t), formWarnings...)}
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
	s.Log.Info("AWX catalog synced", "templates", res.Templates, "created", res.Created, "linked", res.Linked, "gone", res.Gone)
	return res, nil
}

// launchWarnings are the template's launch settings that keep rvav's
// launch from working as meant.
func launchWarnings(t awx.Template) []string {
	var out []string
	if !t.AskVariables {
		out = append(out, "Variables are not prompted on launch: AWX ignores rvav's context and anything outside the survey.")
	}
	if !t.AskLimit {
		out = append(out, "Limit is not prompted on launch: the template can't be run on one VM.")
	}
	return out
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Sync) create(ctx context.Context, t awx.Template, survey []awx.SurveyQuestion) (string, error) {
	name := strings.ReplaceAll(strings.ReplaceAll(t.Name, "-", " "), "_", " ")
	name = strings.ToUpper(name[:1]) + name[1:]
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
	bp := map[string]any{"kind": "awx", "awx": map[string]any{"title": name, "job_template": t.Name, "workflow": t.Workflow}}
	j := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	it := store.NewAWXItem{ID: ids.New(ids.Item), VersionID: ids.New(ids.Version),
		Slug: strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(t.Name), "-"), "-"), Name: name, Summary: summary,
		Category: category, Type: typ, Icon: icon, AWXID: t.ID, AWXName: t.Name,
		Schema: j(schema), UI: j(ui), Initial: j(initial), Blueprint: j(bp)}
	return it.ID, s.Store.CreateAWXItem(ctx, it)
}
