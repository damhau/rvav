package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reports (S13.4): how the platform is used, over requests, approvals,
// deployments and quotas.

// ReportFilter is what a report covers: the projects (all, or those listed),
// one project or environment, and the days [From, To).
type ReportFilter struct {
	All         bool
	Projects    []string
	Project     string
	Environment string
	From, To    time.Time
}

// scope is the filter's condition on a table alias with project_id and
// environment columns; its arguments are $1 to $4, the range $5 and $6.
func scope(alias string) string {
	return strings.NewReplacer("r.", alias+".").Replace(
		`($1 or r.project_id = any($2)) and ($3 = '' or r.project_id = $3) and ($4 = '' or r.environment = $4)`)
}

func (f ReportFilter) args(more ...any) []any {
	projects := f.Projects
	if projects == nil {
		projects = []string{}
	}
	return append([]any{f.All, projects, f.Project, f.Environment, f.From, f.To}, more...)
}

// ReportCounts are requests by how they ended.
type ReportCounts struct {
	Submitted int `db:"submitted"`
	Succeeded int `db:"succeeded"`
	Failed    int `db:"failed"`
	Rejected  int `db:"rejected"`
	Cancelled int `db:"cancelled"`
}

const countCols = `count(*)::int as submitted,
	count(*) filter (where r.status = 'succeeded')::int as succeeded,
	count(*) filter (where r.status = 'failed')::int as failed,
	count(*) filter (where r.status = 'rejected')::int as rejected,
	count(*) filter (where r.status = 'cancelled')::int as cancelled`

// Durations are how long something took, in seconds.
type Durations struct {
	Count int      `db:"count"`
	P50   *float64 `db:"p50"`
	P90   *float64 `db:"p90"`
	Avg   *float64 `db:"avg"`
}

// durationCols aggregates the seconds of expr where cond holds.
func durationCols(expr, cond string) string {
	return `count(*) filter (where ` + cond + `)::int as count,
	percentile_cont(0.5) within group (order by extract(epoch from ` + expr + `)) filter (where ` + cond + `) as p50,
	percentile_cont(0.9) within group (order by extract(epoch from ` + expr + `)) filter (where ` + cond + `) as p90,
	(avg(extract(epoch from ` + expr + `)) filter (where ` + cond + `))::float8 as avg`
}

const built = `r.status = 'succeeded' and r.started_at is not null and r.finished_at is not null`

// ReportPeriod counts the requests submitted in a period starting at Period.
type ReportPeriod struct {
	Period time.Time `db:"period"`
	ReportCounts
}

// RequestsOverTime counts requests by the day, week or month (UTC) they
// were submitted; periods without any are left out.
func (s *Store) RequestsOverTime(ctx context.Context, f ReportFilter, interval string) ([]ReportPeriod, error) {
	rows, _ := s.Pool.Query(ctx, `select date_trunc($7, r.submitted_at at time zone 'UTC') as period, `+countCols+`
		from requests r where `+scope("r")+` and r.submitted_at >= $5 and r.submitted_at < $6
		group by 1 order by 1`, f.args(interval)...)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ReportPeriod])
}

// ReportItem counts the requests of one item and how long they took to build.
type ReportItem struct {
	ID   string `db:"id"`
	Slug string `db:"slug"`
	Name string `db:"name"`
	ReportCounts
	Durations
}

// RequestsByItem lists the items requested most, up to limit.
func (s *Store) RequestsByItem(ctx context.Context, f ReportFilter, limit int) ([]ReportItem, error) {
	rows, _ := s.Pool.Query(ctx, `select i.id, i.slug, i.name, `+countCols+`, `+durationCols("r.finished_at - r.started_at", built)+`
		from requests r join catalog_items i on i.id = r.item_id
		where `+scope("r")+` and r.submitted_at >= $5 and r.submitted_at < $6 and r.kind = 'provision'
		group by i.id, i.slug, i.name order by submitted desc, i.name limit $7`, f.args(limit)...)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ReportItem])
}

// RequestTotals counts every request in the range, and how long new
// deployments took to build.
func (s *Store) RequestTotals(ctx context.Context, f ReportFilter) (ReportCounts, Durations, error) {
	type totals struct {
		ReportCounts
		Durations
	}
	rows, _ := s.Pool.Query(ctx, `select `+countCols+`, `+
		durationCols("r.finished_at - r.started_at", "r.kind = 'provision' and "+built)+`
		from requests r where `+scope("r")+` and r.submitted_at >= $5 and r.submitted_at < $6`, f.args()...)
	t, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[totals])
	return t.ReportCounts, t.Durations, err
}

// ApprovalStats are the levels decided in the range and how long they
// took, and those waiting now.
type ApprovalStats struct {
	Decided  int `db:"decided"`
	Approved int `db:"approved"`
	Rejected int `db:"rejected"`
	Durations
	Pending       int        `db:"pending"`
	OldestPending *time.Time `db:"oldest_pending"`
}

const decidedInRange = `a.status in ('approved', 'rejected') and a.decided_at >= $5 and a.decided_at < $6`

func (s *Store) ApprovalStats(ctx context.Context, f ReportFilter) (ApprovalStats, error) {
	rows, _ := s.Pool.Query(ctx, `select count(*) filter (where `+decidedInRange+`)::int as decided,
		count(*) filter (where `+decidedInRange+` and a.status = 'approved')::int as approved,
		count(*) filter (where `+decidedInRange+` and a.status = 'rejected')::int as rejected,
		`+durationCols("a.decided_at - a.opened_at", decidedInRange+" and a.opened_at is not null")+`,
		count(*) filter (where a.status = 'pending')::int as pending,
		min(a.opened_at) filter (where a.status = 'pending') as oldest_pending
		from approvals a join requests r on r.id = a.request_id where `+scope("r"), f.args()...)
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ApprovalStats])
}

// LeaseRow is a deployment whose lease ends soon.
type LeaseRow struct {
	ID          string    `db:"id"`
	Name        string    `db:"name"`
	ProjectID   string    `db:"project_id"`
	ProjectCode string    `db:"project_code"`
	ProjectName string    `db:"project_name"`
	Environment string    `db:"environment"`
	OwnerID     string    `db:"owner_id"`
	OwnerName   string    `db:"owner_name"`
	ExpiresAt   time.Time `db:"lease_expires_at"`
}

// LeasesEnding lists the live deployments whose lease ends before until,
// soonest first (the filter's range doesn't apply).
func (s *Store) LeasesEnding(ctx context.Context, f ReportFilter, until time.Time, limit int) ([]LeaseRow, error) {
	rows, _ := s.Pool.Query(ctx, `select d.id, d.name, p.id as project_id, p.code as project_code, p.name as project_name,
		d.environment, u.id as owner_id, u.name as owner_name, d.lease_expires_at
		from deployments d join projects p on p.id = d.project_id join users u on u.id = d.owner_id
		where `+scope("d")+` and d.status in ('ready', 'updating', 'failed')
		  and d.lease_expires_at >= now() and d.lease_expires_at < $5
		order by d.lease_expires_at, d.id limit $6`, append(f.args()[:4], until, limit)...)
	return pgx.CollectRows(rows, pgx.RowToStructByName[LeaseRow])
}

// RequestRow is one request, for the CSV export.
type ReportRequestRow struct {
	Number      string     `db:"number"`
	Kind        string     `db:"kind"`
	Title       string     `db:"title"`
	Item        string     `db:"item"`
	Project     string     `db:"project"`
	Environment string     `db:"environment"`
	Requester   string     `db:"requester"`
	Status      string     `db:"status"`
	SubmittedAt time.Time  `db:"submitted_at"`
	StartedAt   *time.Time `db:"started_at"`
	FinishedAt  *time.Time `db:"finished_at"`
}

func (s *Store) ReportRequests(ctx context.Context, f ReportFilter, limit int) ([]ReportRequestRow, error) {
	rows, _ := s.Pool.Query(ctx, `select r.number, r.kind, r.title, i.slug as item, p.code as project, r.environment,
		u.email as requester, r.status, r.submitted_at, r.started_at, r.finished_at
		from requests r join catalog_items i on i.id = r.item_id join projects p on p.id = r.project_id
		join users u on u.id = r.requester_id
		where `+scope("r")+` and r.submitted_at >= $5 and r.submitted_at < $6
		order by r.submitted_at, r.id limit $7`, f.args(limit)...)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ReportRequestRow])
}

// ReportApprovalRow is one approval level, for the CSV export.
type ReportApprovalRow struct {
	Number    string     `db:"number"`
	Seq       int        `db:"seq"`
	Name      string     `db:"name"`
	Status    string     `db:"status"`
	OpenedAt  *time.Time `db:"opened_at"`
	DecidedAt *time.Time `db:"decided_at"`
	Outcome   *string    `db:"outcome"`
}

// ReportApprovals lists the levels decided in the range and those pending.
func (s *Store) ReportApprovals(ctx context.Context, f ReportFilter, limit int) ([]ReportApprovalRow, error) {
	rows, _ := s.Pool.Query(ctx, `select r.number, a.seq, a.name, a.status, a.opened_at, a.decided_at, a.outcome
		from approvals a join requests r on r.id = a.request_id
		where `+scope("r")+` and (`+decidedInRange+` or a.status = 'pending')
		order by coalesce(a.decided_at, a.opened_at), r.number, a.seq limit $7`, f.args(limit)...)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ReportApprovalRow])
}
