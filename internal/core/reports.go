package core

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Reports (S13.4): how the platform is used. Administrators and auditors
// see every project; others the projects they are members of.

// ReportQuery is what a report covers: days (UTC, both included), a
// project and an environment, and the period requests are counted by.
type ReportQuery struct {
	From, To    *time.Time
	ProjectID   string
	Environment string
	Interval    string
}

const (
	maxReportDays = 731
	leaseHorizon  = 30 * 24 * time.Hour
	csvRows       = 100000
)

// reportFilter checks the query and scopes it to what the caller sees.
func (s *Service) reportFilter(p *auth.Principal, q ReportQuery) (store.ReportFilter, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	to, from := today, today.AddDate(0, 0, -29)
	if q.To != nil {
		to = q.To.UTC().Truncate(24 * time.Hour)
	}
	if q.From != nil {
		from = q.From.UTC().Truncate(24 * time.Hour)
	} else if q.To != nil {
		from = to.AddDate(0, 0, -29)
	}
	f := store.ReportFilter{All: p.SeesEverything(), Project: q.ProjectID, Environment: q.Environment, From: from, To: to.AddDate(0, 0, 1)}
	switch {
	case from.After(to):
		return f, &ValidationError{Errors: []api.FieldError{fieldErr("/from", "invalid", "The first day comes after the last.")}}
	case to.Sub(from) > maxReportDays*24*time.Hour:
		return f, &ValidationError{Errors: []api.FieldError{fieldErr("/from", "too_long", "A report covers two years at most.")}}
	}
	switch q.Interval {
	case "", "day", "week", "month":
	default:
		return f, &ValidationError{Errors: []api.FieldError{fieldErr("/interval", "enum", "day, week or month.")}}
	}
	if !f.All {
		f.Projects = slices.Sorted(maps.Keys(p.ProjectRoles))
		if q.ProjectID != "" && !slices.Contains(f.Projects, q.ProjectID) {
			return f, ErrForbidden
		}
	}
	return f, nil
}

// Report answers GET /reports.
func (s *Service) Report(ctx context.Context, p *auth.Principal, q ReportQuery) (api.Report, error) {
	var out api.Report
	f, err := s.reportFilter(p, q)
	if err != nil {
		return out, err
	}
	interval := orDefault(q.Interval, "day")
	out.From, out.To, out.Interval = openapi_types.Date{Time: f.From}, openapi_types.Date{Time: f.To.AddDate(0, 0, -1)}, api.ReportInterval(interval)

	totals, built, err := s.Store.RequestTotals(ctx, f)
	if err != nil {
		return out, err
	}
	out.Requests.Totals, out.Provisioning = presentCounts(totals), presentDurations(built)

	periods, err := s.Store.RequestsOverTime(ctx, f, interval)
	if err != nil {
		return out, err
	}
	byPeriod := map[time.Time]store.ReportCounts{}
	for _, r := range periods {
		byPeriod[r.Period.UTC()] = r.ReportCounts
	}
	out.Requests.OverTime = []api.ReportPeriod{}
	for t := periodStart(f.From, interval); t.Before(f.To); t = nextPeriod(t, interval) {
		c := presentCounts(byPeriod[t])
		out.Requests.OverTime = append(out.Requests.OverTime, api.ReportPeriod{Period: openapi_types.Date{Time: t},
			Submitted: c.Submitted, Succeeded: c.Succeeded, Failed: c.Failed, Rejected: c.Rejected, Cancelled: c.Cancelled,
			SuccessRate: c.SuccessRate})
	}

	items, err := s.Store.RequestsByItem(ctx, f, 10)
	if err != nil {
		return out, err
	}
	out.Requests.ByItem = []api.ReportItem{}
	for _, it := range items {
		c := presentCounts(it.ReportCounts)
		out.Requests.ByItem = append(out.Requests.ByItem, api.ReportItem{Item: api.CatalogItemRef{Id: it.ID, Slug: it.Slug, Name: it.Name},
			Submitted: c.Submitted, Succeeded: c.Succeeded, Failed: c.Failed, Rejected: c.Rejected, Cancelled: c.Cancelled,
			SuccessRate: c.SuccessRate, Provisioning: presentDurations(it.Durations)})
	}

	ap, err := s.Store.ApprovalStats(ctx, f)
	if err != nil {
		return out, err
	}
	out.Approvals = api.ReportApprovals{Decided: ap.Decided, Approved: ap.Approved, Rejected: ap.Rejected,
		LeadTime: presentDurations(ap.Durations), Pending: ap.Pending, OldestPendingSince: ap.OldestPending}

	if out.Quota, err = s.quotaLines(ctx, f); err != nil {
		return out, err
	}

	leases, err := s.Store.LeasesEnding(ctx, f, time.Now().Add(leaseHorizon), 200)
	if err != nil {
		return out, err
	}
	out.Leases = []api.ReportLease{}
	for _, l := range leases {
		out.Leases = append(out.Leases, api.ReportLease{Deployment: api.DeploymentRef{Id: l.ID, Name: l.Name},
			Project: api.ProjectRef{Id: l.ProjectID, Code: l.ProjectCode, Name: l.ProjectName}, Environment: l.Environment,
			Owner: api.UserRef{Id: l.OwnerID, Name: l.OwnerName}, ExpiresAt: l.ExpiresAt})
	}
	return out, nil
}

func presentCounts(c store.ReportCounts) api.ReportCounts {
	out := api.ReportCounts{Submitted: c.Submitted, Succeeded: c.Succeeded, Failed: c.Failed, Rejected: c.Rejected, Cancelled: c.Cancelled}
	if ended := c.Succeeded + c.Failed; ended > 0 {
		out.SuccessRate = present.Ptr(float32(c.Succeeded) / float32(ended))
	}
	return out
}

func presentDurations(d store.Durations) api.ReportDurations {
	f := func(v *float64) *float32 {
		if v == nil {
			return nil
		}
		return present.Ptr(float32(*v))
	}
	return api.ReportDurations{Count: d.Count, P50Seconds: f(d.P50), P90Seconds: f(d.P90), AverageSeconds: f(d.Avg)}
}

// periodStart and nextPeriod follow PostgreSQL's date_trunc: weeks start
// on Monday.
func periodStart(t time.Time, interval string) time.Time {
	t = t.UTC().Truncate(24 * time.Hour)
	switch interval {
	case "week":
		return t.AddDate(0, 0, -(int(t.Weekday())+6)%7)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}

func nextPeriod(t time.Time, interval string) time.Time {
	switch interval {
	case "week":
		return t.AddDate(0, 0, 7)
	case "month":
		return t.AddDate(0, 1, 0)
	}
	return t.AddDate(0, 0, 1)
}

// quotaLines are each project's usage now: project-wide for every resource,
// and per environment where it has a limit (every resource for the
// environment asked for).
func (s *Service) quotaLines(ctx context.Context, f store.ReportFilter) ([]api.ReportQuotaLine, error) {
	out := []api.ReportQuotaLine{}
	var projects []store.Project
	if f.All {
		page, err := s.Store.ListProjects(ctx, nil, 1000, nil)
		if err != nil {
			return nil, err
		}
		projects = page.Items
	} else {
		byID, err := s.Store.ProjectsByID(ctx, f.Projects)
		if err != nil {
			return nil, err
		}
		for _, id := range f.Projects {
			if p, ok := byID[id]; ok {
				projects = append(projects, p)
			}
		}
		slices.SortFunc(projects, func(a, b store.Project) int { return strings.Compare(a.Name, b.Name) })
	}
	for _, pr := range projects {
		if (f.Project != "" && pr.ID != f.Project) || pr.ArchivedAt != nil {
			continue
		}
		scopes := []string{""}
		if f.Environment != "" {
			scopes = []string{f.Environment}
		} else {
			scopes = append(scopes, pr.Environments...)
		}
		for _, scope := range scopes {
			limits, err := s.Store.QuotaLimits(ctx, pr.ID, scope)
			if err != nil {
				return nil, err
			}
			if scope != "" && f.Environment == "" && len(limits) == 0 {
				continue
			}
			used, err := s.Store.QuotaUsed(ctx, pr.ID, scope)
			if err != nil {
				return nil, err
			}
			for _, res := range quotaOrder {
				line := api.ReportQuotaLine{Project: api.ProjectRef{Id: pr.ID, Code: pr.Code, Name: pr.Name},
					Resource: api.QuotaResource(res), Used: used[res]}
				if scope != "" {
					line.Environment = present.Ptr(scope)
				}
				i := slices.IndexFunc(limits, func(l store.QuotaLimit) bool { return l.Resource == res })
				if i >= 0 {
					line.Limit = limits[i].Limit
				} else if scope != "" && f.Environment == "" {
					continue
				}
				out = append(out, line)
			}
		}
	}
	return out, nil
}

// ReportCSV is one section of the report as CSV rows, a header first: one
// row per request, approval level, quota line or lease.
func (s *Service) ReportCSV(ctx context.Context, p *auth.Principal, q ReportQuery, section string) ([][]string, error) {
	f, err := s.reportFilter(p, q)
	if err != nil {
		return nil, err
	}
	stamp := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	seconds := func(from, to *time.Time) string {
		if from == nil || to == nil {
			return ""
		}
		return strconv.FormatFloat(to.Sub(*from).Seconds(), 'f', 0, 64)
	}
	var rows [][]string
	switch section {
	case "requests":
		list, err := s.Store.ReportRequests(ctx, f, csvRows)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []string{"number", "kind", "title", "item", "project", "environment", "requester", "status",
			"submitted_at", "started_at", "finished_at", "run_seconds"})
		for _, r := range list {
			rows = append(rows, []string{r.Number, r.Kind, r.Title, r.Item, r.Project, r.Environment, r.Requester, r.Status,
				stamp(&r.SubmittedAt), stamp(r.StartedAt), stamp(r.FinishedAt), seconds(r.StartedAt, r.FinishedAt)})
		}
	case "approvals":
		list, err := s.Store.ReportApprovals(ctx, f, csvRows)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []string{"request", "level", "name", "status", "opened_at", "decided_at", "lead_seconds", "outcome"})
		for _, a := range list {
			outcome := ""
			if a.Outcome != nil {
				outcome = *a.Outcome
			}
			rows = append(rows, []string{a.Number, strconv.Itoa(a.Seq), a.Name, a.Status, stamp(a.OpenedAt), stamp(a.DecidedAt),
				seconds(a.OpenedAt, a.DecidedAt), outcome})
		}
	case "quota":
		lines, err := s.quotaLines(ctx, f)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []string{"project", "environment", "resource", "limit", "used"})
		for _, l := range lines {
			env, limit := "", ""
			if l.Environment != nil {
				env = *l.Environment
			}
			if l.Limit != nil {
				limit = strconv.Itoa(*l.Limit)
			}
			rows = append(rows, []string{l.Project.Code, env, string(l.Resource), limit, strconv.Itoa(l.Used)})
		}
	case "leases":
		list, err := s.Store.LeasesEnding(ctx, f, time.Now().Add(leaseHorizon), csvRows)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []string{"deployment", "project", "environment", "owner", "expires_at"})
		for _, l := range list {
			rows = append(rows, []string{l.Name, l.ProjectCode, l.Environment, l.OwnerName, stamp(&l.ExpiresAt)})
		}
	default:
		return nil, &ValidationError{Errors: []api.FieldError{fieldErr("/section", "enum", "requests, approvals, quota or leases.")}}
	}
	return rows, nil
}
