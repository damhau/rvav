package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	// A schedule's time zone is checked where no zoneinfo is installed too.
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// ScheduleControl makes and controls the Temporal schedules that fire
// recurring schedules' runs (provision.Temporal, ADR-0057).
type ScheduleControl interface {
	CreateSchedule(ctx context.Context, id string, s provision.ScheduleSpec) error
	PauseSchedule(ctx context.Context, id, note string) error
	ResumeSchedule(ctx context.Context, id string) error
	DeleteSchedule(ctx context.Context, id string) error
	NextRuns(ctx context.Context, id string) ([]time.Time, error)
	// ListSchedules gives the schedules Temporal has, by id: paused or not.
	ListSchedules(ctx context.Context) (map[string]bool, error)
}

var cronField = regexp.MustCompile(`^[0-9A-Za-z*,/\-?#]+$`)

const maxNextRuns = 5

// errFiredAlready is a firing whose request was submitted already.
var errFiredAlready = errors.New("this firing has run already")

func canSeeSchedule(p *auth.Principal, sc store.ScheduleRow) bool {
	return canManageSchedule(p, sc) || p.In(auth.DeploymentsReadAll, sc.ProjectID)
}

func canManageSchedule(p *auth.Principal, sc store.ScheduleRow) bool {
	return sc.OwnerID == p.User.ID || p.In(auth.RequestManage, sc.ProjectID)
}

// ListSchedules lists the schedules the caller owns, and those of the
// projects whose requests they manage or whose deployments they all see.
func (s *Service) ListSchedules(ctx context.Context, p *auth.Principal, projectID *string) ([]api.Schedule, error) {
	list, err := s.Store.Schedules(ctx)
	if err != nil {
		return nil, err
	}
	out := []api.Schedule{}
	for _, sc := range list {
		if !canSeeSchedule(p, sc) || projectID != nil && *projectID != sc.ProjectID {
			continue
		}
		out = append(out, presentSchedule(sc, s.nextRuns(ctx, sc)))
	}
	return out, nil
}

// Schedule is one schedule with its last runs.
func (s *Service) Schedule(ctx context.Context, p *auth.Principal, id string) (api.Schedule, error) {
	sc, err := s.Store.GetSchedule(ctx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && !canSeeSchedule(p, sc) {
		return api.Schedule{}, ErrNotFound
	} else if err != nil {
		return api.Schedule{}, err
	}
	return s.scheduleWithRuns(ctx, sc)
}

func (s *Service) scheduleWithRuns(ctx context.Context, sc store.ScheduleRow) (api.Schedule, error) {
	runs, err := s.Store.ScheduleRuns(ctx, sc.ID, 20)
	if err != nil {
		return api.Schedule{}, err
	}
	out := presentSchedule(sc, s.nextRuns(ctx, sc))
	list := make([]api.ScheduleRun, len(runs))
	for i, r := range runs {
		list[i] = presentScheduleRun(r)
	}
	out.Runs = &list
	return out, nil
}

// nextRuns asks Temporal; a schedule shows without them when it can't answer.
func (s *Service) nextRuns(ctx context.Context, sc store.ScheduleRow) []time.Time {
	if s.ScheduleControl == nil || sc.Paused {
		return []time.Time{}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	next, err := s.ScheduleControl.NextRuns(ctx, sc.ID)
	if err != nil {
		s.Log.Warn("a schedule's next runs", "schedule", sc.ID, "err", err)
		return []time.Time{}
	}
	if len(next) > maxNextRuns {
		next = next[:maxNextRuns]
	}
	return append([]time.Time{}, next...)
}

func presentSchedule(sc store.ScheduleRow, next []time.Time) api.Schedule {
	out := api.Schedule{Id: sc.ID, Name: sc.Name, Version: api.CatalogItemVersionRef{Id: sc.VersionID, Number: sc.VersionNumber},
		Project: present.ProjectRef(sc.ProjectID, sc.ProjectCode, sc.ProjectName), Environment: sc.Environment,
		Owner: present.UserRef(sc.OwnerID, sc.OwnerName, sc.OwnerEmail), OwnerSeenAt: sc.OwnerSeenAt, Inputs: map[string]any{}, Cron: sc.Cron,
		EveryMinutes: sc.EveryMinutes, Timezone: sc.Timezone, Paused: sc.Paused, CreatedAt: sc.CreatedAt, NextRuns: next,
		AcceptWarnings: present.Ptr(sc.AcceptWarnings)}
	out.CatalogItem.Id, out.CatalogItem.Slug, out.CatalogItem.Name = sc.ItemID, sc.ItemSlug, sc.ItemName
	out.CatalogItem.Type = api.CatalogItemType(sc.ItemType)
	_ = json.Unmarshal(sc.Inputs, &out.Inputs)
	if sc.Justification != "" {
		out.Justification = &sc.Justification
	}
	if sc.Paused && sc.PausedNote != "" {
		out.PausedNote = &sc.PausedNote
	}
	if sc.LastRunAt != nil && sc.LastOutcome != nil {
		last := presentScheduleRun(store.ScheduleRun{At: *sc.LastRunAt, Outcome: *sc.LastOutcome, Reason: deref(sc.LastReason),
			RequestID: sc.LastRequestID, RequestNumber: sc.LastNumber, RequestStatus: sc.LastStatus})
		out.LastRun = &last
	}
	return out
}

func presentScheduleRun(r store.ScheduleRun) api.ScheduleRun {
	out := api.ScheduleRun{At: r.At, Outcome: api.ScheduleRunOutcome(r.Outcome)}
	if r.Reason != "" {
		out.Reason = &r.Reason
	}
	if r.RequestID != nil && r.RequestNumber != nil && r.RequestStatus != nil {
		out.Request = &struct {
			Id     string            `json:"id"`
			Number string            `json:"number"`
			Status api.RequestStatus `json:"status"`
		}{Id: *r.RequestID, Number: *r.RequestNumber, Status: api.RequestStatus(*r.RequestStatus)}
	}
	return out
}

// scheduleSpec checks when a schedule runs.
func scheduleSpec(w api.ScheduleWrite) (provision.ScheduleSpec, []api.FieldError) {
	spec := provision.ScheduleSpec{Cron: strings.Join(strings.Fields(deref(w.Cron)), " "), Timezone: strings.TrimSpace(deref(w.Timezone))}
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	var errs []api.FieldError
	switch {
	case spec.Cron != "" && w.EveryMinutes != nil:
		errs = append(errs, fieldErr("/cron", "one_of", "Give a cron expression or a number of minutes, not both."))
	case spec.Cron == "" && w.EveryMinutes == nil:
		errs = append(errs, fieldErr("/cron", "required", "Say when it runs: a cron expression, or every so many minutes."))
	case w.EveryMinutes != nil:
		if *w.EveryMinutes < 5 || *w.EveryMinutes > 44640 {
			errs = append(errs, fieldErr("/every_minutes", "out_of_range", "From every 5 minutes to every 31 days (44640 minutes)."))
		}
		spec.EveryMinutes = *w.EveryMinutes
	default:
		fields := strings.Fields(spec.Cron)
		if len(fields) != 5 || slices.ContainsFunc(fields, func(f string) bool { return !cronField.MatchString(f) }) {
			errs = append(errs, fieldErr("/cron", "pattern", "Five fields: minute, hour, day of month, month and day of week, such as 0 2 * * 1-5 for 2:00 on weekdays."))
		}
	}
	if _, err := time.LoadLocation(spec.Timezone); err != nil {
		errs = append(errs, fieldErr("/timezone", "unknown", "Not a time zone rvav knows: an IANA name, such as Europe/Zurich."))
	}
	return spec, errs
}

func scheduleDraft(sc store.Schedule) api.RequestDraft {
	d := api.RequestDraft{CatalogItemId: sc.ItemID, VersionId: sc.VersionID, ProjectId: sc.ProjectID,
		Environment: sc.Environment, Inputs: map[string]any{}, AcceptWarnings: present.Ptr(sc.AcceptWarnings)}
	_ = json.Unmarshal(sc.Inputs, &d.Inputs)
	if sc.Justification != "" {
		d.Justification = &sc.Justification
	}
	return d
}

// CreateSchedule makes a recurring schedule of an item that makes no
// deployment, owned by the caller: its draft is checked as a request's.
func (s *Service) CreateSchedule(ctx context.Context, p *auth.Principal, w api.ScheduleWrite) (api.Schedule, error) {
	if s.ScheduleControl == nil {
		return api.Schedule{}, &UpstreamError{System: "Temporal", Err: errors.New("rvav has no Temporal to run schedules")}
	}
	sc := store.Schedule{ID: ids.New(ids.Schedule), Name: strings.TrimSpace(w.Name), ItemID: w.CatalogItemId, VersionID: w.VersionId,
		ProjectID: w.ProjectId, Environment: w.Environment, OwnerID: p.User.ID, Justification: strings.TrimSpace(deref(w.Justification)),
		AcceptWarnings: w.AcceptWarnings != nil && *w.AcceptWarnings}
	spec, errs := scheduleSpec(w)
	sc.Cron, sc.Timezone = nonEmpty(spec.Cron), spec.Timezone
	if w.EveryMinutes != nil {
		sc.EveryMinutes = w.EveryMinutes
	}
	switch {
	case sc.Name == "":
		errs = append(errs, fieldErr("/name", "required", "Name it, in 120 characters at most."))
	case utf8.RuneCountInString(sc.Name) > 120:
		errs = append(errs, fieldErr("/name", "too_long", "Name it in 120 characters at most."))
	}
	inputs := w.Inputs
	if inputs == nil {
		inputs = map[string]any{}
	}
	sc.Inputs, _ = json.Marshal(inputs)
	ev, err := s.Evaluate(ctx, p, scheduleDraft(sc), true)
	if err != nil {
		return api.Schedule{}, err
	}
	errs = append(errs, ev.Errors...)
	if len(ev.Errors) == 0 {
		makes, err := s.MakesDeployments(ctx, ev.Version.Blueprint)
		if err != nil {
			return api.Schedule{}, err
		}
		if makes {
			errs = append(errs, fieldErr("/catalog_item_id", "makes_deployments",
				ev.Item.Name+" makes a deployment, each run would make another: only an item that makes none, such as a custom workflow, runs on a schedule."))
		}
		for field := range present.SecretFields(ev.Version.UISchema) {
			if v, ok := inputs[field]; ok && v != nil && v != "" {
				errs = append(errs, fieldErr("/inputs/"+field, "secret_input",
					"A schedule doesn't keep secret inputs: leave it empty, or have the item read a stored secret."))
			}
		}
	}
	if len(errs) > 0 {
		return api.Schedule{}, &ValidationError{Errors: errs}
	}
	if err := accepted(ev.Warnings, w.AcceptWarnings, nil); err != nil {
		return api.Schedule{}, err
	}
	if err := s.Store.InsertSchedule(ctx, sc); err != nil {
		return api.Schedule{}, err
	}
	if err := s.ScheduleControl.CreateSchedule(ctx, sc.ID, spec); err != nil {
		if rerr := s.Store.RemoveSchedule(context.WithoutCancel(ctx), sc.ID); rerr != nil {
			s.Log.Error("remove a schedule not made", "schedule", sc.ID, "err", rerr)
		}
		var invalid *provision.InvalidScheduleError
		if errors.As(err, &invalid) {
			field := "/cron"
			if spec.Cron == "" {
				field = "/every_minutes"
			}
			return api.Schedule{}, &ValidationError{Errors: []api.FieldError{fieldErr(field, "invalid", "Temporal refuses it: "+invalid.Msg)}}
		}
		return api.Schedule{}, &UpstreamError{System: "Temporal", Err: err}
	}
	s.audit(ctx, p, "schedule.create", "schedule", sc.ID, &sc.ProjectID, map[string]any{"name": sc.Name, "item": ev.Item.Slug,
		"version": ev.Version.Number, "environment": sc.Environment, "cron": spec.Cron, "every_minutes": sc.EveryMinutes, "timezone": sc.Timezone})
	return s.Schedule(ctx, p, sc.ID)
}

// manageable is a schedule the caller may pause, resume or delete: theirs,
// or one of a project whose requests they manage.
func (s *Service) manageable(ctx context.Context, p *auth.Principal, id string) (store.ScheduleRow, error) {
	sc, err := s.Store.GetSchedule(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && !canSeeSchedule(p, sc) && !p.In(auth.ProjectSee, sc.ProjectID):
		return sc, ErrNotFound
	case err != nil:
		return sc, err
	case !canManageSchedule(p, sc):
		return sc, fmt.Errorf("%w: only its owner or a project admin can change this schedule", ErrForbidden)
	case s.ScheduleControl == nil:
		return sc, &UpstreamError{System: "Temporal", Err: errors.New("rvav has no Temporal to run schedules")}
	}
	return sc, nil
}

// PauseSchedule stops a schedule's runs until it is resumed.
func (s *Service) PauseSchedule(ctx context.Context, p *auth.Principal, id string, note string) (api.Schedule, error) {
	sc, err := s.manageable(ctx, p, id)
	if err != nil {
		return api.Schedule{}, err
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 500 {
		return api.Schedule{}, &ValidationError{Errors: []api.FieldError{fieldErr("/note", "too_long", "500 characters at most.")}}
	}
	if err := s.ScheduleControl.PauseSchedule(ctx, sc.ID, note); err != nil {
		return api.Schedule{}, &UpstreamError{System: "Temporal", Err: err}
	}
	if err := s.Store.SetSchedulePaused(ctx, sc.ID, true, note); err != nil {
		return api.Schedule{}, err
	}
	s.audit(ctx, p, "schedule.pause", "schedule", sc.ID, &sc.ProjectID, map[string]any{"name": sc.Name, "note": note})
	return s.Schedule(ctx, p, sc.ID)
}

// ResumeSchedule starts a paused schedule's runs again, from its next time.
func (s *Service) ResumeSchedule(ctx context.Context, p *auth.Principal, id string) (api.Schedule, error) {
	sc, err := s.manageable(ctx, p, id)
	if err != nil {
		return api.Schedule{}, err
	}
	if err := s.ScheduleControl.ResumeSchedule(ctx, sc.ID); err != nil {
		return api.Schedule{}, &UpstreamError{System: "Temporal", Err: err}
	}
	if err := s.Store.SetSchedulePaused(ctx, sc.ID, false, ""); err != nil {
		return api.Schedule{}, err
	}
	s.audit(ctx, p, "schedule.resume", "schedule", sc.ID, &sc.ProjectID, map[string]any{"name": sc.Name})
	return s.Schedule(ctx, p, sc.ID)
}

// DeleteSchedule ends a schedule; the requests it started stay.
func (s *Service) DeleteSchedule(ctx context.Context, p *auth.Principal, id string) error {
	sc, err := s.manageable(ctx, p, id)
	if err != nil {
		return err
	}
	if err := s.ScheduleControl.DeleteSchedule(ctx, sc.ID); err != nil {
		return &UpstreamError{System: "Temporal", Err: err}
	}
	if err := s.Store.DeleteSchedule(ctx, sc.ID); err != nil {
		return err
	}
	s.auditGone(ctx, p, "schedule.delete", "schedule", sc.ID, sc.Name, &sc.ProjectID, map[string]any{"name": sc.Name})
	return nil
}

// ReconcileSchedules makes Temporal's schedules agree with rvav's, at a
// worker's start: the database row and the Temporal schedule aren't written
// at once, so a process that stopped in between left one without the other,
// or a pause in Temporal only. rvav's rows are what callers were told.
func (s *Service) ReconcileSchedules(ctx context.Context) error {
	if s.ScheduleControl == nil {
		return nil
	}
	// Temporal's first: a schedule made in between has its row already.
	have, err := s.ScheduleControl.ListSchedules(ctx)
	if err != nil {
		return err
	}
	rows, err := s.Store.Schedules(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sc := range rows {
		paused, ok := have[sc.ID]
		delete(have, sc.ID)
		switch {
		case !ok:
			spec := provision.ScheduleSpec{Cron: deref(sc.Cron), Timezone: sc.Timezone, Paused: sc.Paused, Note: sc.PausedNote}
			if sc.EveryMinutes != nil {
				spec.EveryMinutes = *sc.EveryMinutes
			}
			s.Log.Info("schedule made again in Temporal", "schedule", sc.ID)
			errs = append(errs, s.ScheduleControl.CreateSchedule(ctx, sc.ID, spec))
		case paused && !sc.Paused:
			errs = append(errs, s.ScheduleControl.ResumeSchedule(ctx, sc.ID))
		case !paused && sc.Paused:
			errs = append(errs, s.ScheduleControl.PauseSchedule(ctx, sc.ID, sc.PausedNote))
		}
	}
	// Only those rvav deleted: a namespace may hold another rvav's.
	deleted, err := s.Store.DeletedSchedules(ctx, slices.Collect(maps.Keys(have)))
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, id := range deleted {
		s.Log.Info("deleted schedule removed from Temporal", "schedule", id)
		errs = append(errs, s.ScheduleControl.DeleteSchedule(ctx, id))
	}
	return errors.Join(errs...)
}

// scheduleBusyError is a firing while a request the schedule started
// hasn't finished: another firing's, submitted at the same time.
type scheduleBusyError struct{ number string }

func (e *scheduleBusyError) Error() string { return e.number + " hadn't finished" }

// ownerSeen is when a schedule's owner was last seen: making it counts.
func ownerSeen(sc store.ScheduleRow) time.Time {
	if sc.OwnerSeenAt != nil && sc.OwnerSeenAt.After(sc.CreatedAt) {
		return *sc.OwnerSeenAt
	}
	return sc.CreatedAt
}

// RunSchedule submits a firing's request as the schedule's owner, as
// POST /requests would: what refuses a request refuses it, and the firing
// keeps why. It skips a firing while a request the schedule started hasn't
// finished, and while its owner hasn't been seen for
// RVAV_SCHEDULE_OWNER_IDLE_DAYS.
// An error is one to try again.
func (s *Service) RunSchedule(ctx context.Context, f provision.ScheduleFiring) error {
	sc, err := s.Store.GetSchedule(ctx, f.ScheduleID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := s.Store.GetScheduleRun(ctx, f.FireID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	record := func(outcome, reason string) error {
		_, err := s.Store.InsertScheduleRun(ctx, s.Store.Pool, store.ScheduleRun{FireID: f.FireID, ScheduleID: sc.ID, At: f.At,
			Outcome: outcome, Reason: reason})
		return err
	}
	if sc.Paused {
		return record("skipped", "It was paused.")
	}
	if number, err := s.Store.UnfinishedScheduleRequest(ctx, s.Store.Pool, sc.ID); err != nil {
		return err
	} else if number != "" {
		return record("skipped", number+" hadn't finished.")
	}
	if idle := s.Config.ScheduleOwnerIdleDays; idle > 0 && time.Since(ownerSeen(sc)) > time.Duration(idle)*24*time.Hour {
		return record("skipped", fmt.Sprintf("Its owner hasn't signed in for %d days: a run would have the groups of their last sign-in. It runs again once they sign in.", idle))
	}
	owner, err := auth.ForUser(ctx, s.Store, sc.OwnerID)
	if errors.Is(err, store.ErrNotFound) {
		return record("refused", "Its owner is no longer known to rvav.")
	} else if err != nil {
		return err
	}
	_, err = s.submit(ctx, owner, scheduleDraft(sc.Schedule), origin{fire: &f})
	var invalid *ValidationError
	var conflict *ConflictError
	var busy *scheduleBusyError
	switch {
	case err == nil, errors.Is(err, errFiredAlready):
		return nil
	case errors.As(err, &busy):
		return record("skipped", busy.number+" hadn't finished.")
	case errors.As(err, &invalid), errors.As(err, &conflict), errors.Is(err, ErrForbidden), errors.Is(err, ErrNotFound):
		return record("refused", refusal(err))
	}
	return err
}

// refusal says why a request was refused, in one line.
func refusal(err error) string {
	var invalid *ValidationError
	if errors.As(err, &invalid) {
		msgs := make([]string, 0, len(invalid.Errors))
		for _, e := range invalid.Errors {
			if !slices.Contains(msgs, e.Message) {
				msgs = append(msgs, e.Message)
			}
		}
		return strings.Join(msgs, " ")
	}
	if errors.Is(err, ErrNotFound) {
		return "Its owner no longer sees the item or the project."
	}
	return err.Error()
}

// ScheduleFailed records a firing rvav couldn't submit, once its tries ran out.
func (s *Service) ScheduleFailed(ctx context.Context, f provision.ScheduleFiring) error {
	_, err := s.Store.InsertScheduleRun(ctx, s.Store.Pool, store.ScheduleRun{FireID: f.FireID, ScheduleID: f.ScheduleID, At: f.At,
		Outcome: "failed", Reason: f.Reason})
	var fk interface{ SQLState() string }
	if errors.As(err, &fk) && fk.SQLState() == "23503" {
		return nil
	}
	return err
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
