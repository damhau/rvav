package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Approval is one level of a request's approval chain (ADR-0011).
type Approval struct {
	ID             string     `db:"id"`
	RequestID      string     `db:"request_id"`
	Seq            int        `db:"seq"`
	Name           string     `db:"name"`
	GroupName      *string    `db:"group_name"`
	ApproverIDs    []string   `db:"approver_ids"`
	Mode           string     `db:"mode"`
	PolicyIDs      []string   `db:"policy_ids"`
	Reasons        []string   `db:"reasons"`
	Rules          []string   `db:"rules"`
	Status         string     `db:"status"`
	TimeoutSeconds *int       `db:"timeout_seconds"`
	OnTimeout      *string    `db:"on_timeout"`
	EscalateGroup  *string    `db:"escalate_group"`
	EscalateIDs    []string   `db:"escalate_ids"`
	Escalated      bool       `db:"escalated"`
	CreatedAt      time.Time  `db:"created_at"`
	OpenedAt       *time.Time `db:"opened_at"`
	DueAt          *time.Time `db:"due_at"`
	DecidedAt      *time.Time `db:"decided_at"`
	Outcome        *string    `db:"outcome"`
}

// Groups returns the groups whose members may decide: the level's, and the
// escalation group once escalated.
func (a Approval) Groups() []string {
	var g []string
	if a.GroupName != nil && *a.GroupName != "" {
		g = append(g, *a.GroupName)
	}
	if a.Escalated && a.EscalateGroup != nil && *a.EscalateGroup != "" {
		g = append(g, *a.EscalateGroup)
	}
	return g
}

// NamedApprovers returns the users named on the level, with the escalation
// approvers once escalated.
func (a Approval) NamedApprovers() []string {
	ids := append([]string{}, a.ApproverIDs...)
	if a.Escalated {
		ids = append(ids, a.EscalateIDs...)
	}
	return ids
}

type ApprovalVote struct {
	ApprovalID string          `db:"approval_id"`
	UserID     string          `db:"user_id"`
	UserName   string          `db:"user_name"`
	UserEmail  string          `db:"user_email"`
	Decision   string          `db:"decision"`
	Comment    string          `db:"comment"`
	Changes    json.RawMessage `db:"changes"`
	DecidedAt  time.Time       `db:"decided_at"`
}

type ApprovalDelegate struct {
	ApprovalID  string    `db:"approval_id"`
	UserID      string    `db:"user_id"`
	UserName    string    `db:"user_name"`
	UserEmail   string    `db:"user_email"`
	DelegatedBy string    `db:"delegated_by"`
	Comment     string    `db:"comment"`
	CreatedAt   time.Time `db:"created_at"`
}

const approvalCols = `a.id, a.request_id, a.seq, a.name, a.group_name, a.approver_ids, a.mode, a.policy_ids,
	a.reasons, a.rules, a.status, a.timeout_seconds, a.on_timeout, a.escalate_group, a.escalate_ids,
	a.escalated, a.created_at, a.opened_at, a.due_at, a.decided_at, a.outcome`

func (s *Store) InsertApprovals(ctx context.Context, db DB, list []Approval) error {
	for _, a := range list {
		_, err := db.Exec(ctx, `insert into approvals (id, request_id, seq, name, group_name, approver_ids, mode,
			policy_ids, reasons, rules, status, timeout_seconds, on_timeout, escalate_group, escalate_ids)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			a.ID, a.RequestID, a.Seq, a.Name, a.GroupName, nonNil(a.ApproverIDs), a.Mode, nonNil(a.PolicyIDs),
			nonNil(a.Reasons), nonNil(a.Rules), a.Status, a.TimeoutSeconds, a.OnTimeout, a.EscalateGroup,
			nonNil(a.EscalateIDs))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetApproval(ctx context.Context, db DB, id string) (Approval, error) {
	rows, _ := db.Query(ctx, `select `+approvalCols+` from approvals a where a.id = $1`, id)
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Approval])
	return a, notFound(err)
}

// LockApproval reads a level for update, so a decision and a timeout can't
// both apply.
func (s *Store) LockApproval(ctx context.Context, tx pgx.Tx, id string) (Approval, error) {
	rows, _ := tx.Query(ctx, `select `+approvalCols+` from approvals a where a.id = $1 for update`, id)
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Approval])
	return a, notFound(err)
}

func (s *Store) ApprovalsForRequest(ctx context.Context, requestID string) ([]Approval, error) {
	rows, _ := s.Pool.Query(ctx, `select `+approvalCols+` from approvals a where a.request_id = $1 order by a.seq`, requestID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Approval])
}

// ApprovalFilter selects approvals for the inbox.
type ApprovalFilter struct {
	// All lists every approval; otherwise the open levels ViewerID may decide
	// (named, in a group, delegated; not once they voted) and the levels they
	// decided, never on their own requests.
	All      bool
	ViewerID string
	Groups   []string
	Statuses []string
	Limit    int
	After    *Cursor
}

// ListApprovals returns a page and the total matching the filter. Pending
// levels come oldest first, the most urgent on top; others newest first.
func (s *Store) ListApprovals(ctx context.Context, f ApprovalFilter) (Page[Approval], int, error) {
	voted := `exists (select 1 from approval_votes v where v.approval_id = a.id and v.user_id = $2)`
	where := `($1 or (r.requester_id <> $2 and (
			-- open levels you may decide (with mode all, not once you voted)
			(a.status in ('waiting', 'pending') and not ` + voted + ` and ($2 = any(a.approver_ids) or a.group_name = any($3)
				or (a.escalated and ($2 = any(a.escalate_ids) or a.escalate_group = any($3)))
				or exists (select 1 from approval_delegates d where d.approval_id = a.id and d.user_id = $2)))
			-- and the ones you decided
			or (a.status not in ('waiting', 'pending') and ` + voted + `))))
		and (cardinality($4::text[]) = 0 or a.status = any($4))`
	args := []any{f.All, f.ViewerID, nonNil(f.Groups), nonNil(f.Statuses)}
	var total int
	if err := s.Pool.QueryRow(ctx, `select count(*) from approvals a join requests r on r.id = a.request_id where `+where,
		args...).Scan(&total); err != nil {
		return Page[Approval]{}, 0, err
	}
	pendingOnly := len(f.Statuses) == 1 && f.Statuses[0] == "pending"
	sortKey, order, cmp := `coalesce(a.decided_at, a.created_at)`, `desc`, `<`
	if pendingOnly {
		sortKey, order, cmp = `coalesce(a.opened_at, a.created_at)`, `asc`, `>`
	}
	afterTime, afterID := time.Time{}, ""
	if f.After != nil {
		t, err := time.Parse(time.RFC3339Nano, f.After.Value)
		if err != nil {
			return Page[Approval]{}, 0, ErrBadCursor
		}
		afterTime, afterID = t, f.After.ID
	}
	rows, _ := s.Pool.Query(ctx, `select `+approvalCols+` from approvals a join requests r on r.id = a.request_id
		where `+where+` and ($5 = '' or (`+sortKey+`, a.id) `+cmp+` ($6, $5))
		order by `+sortKey+` `+order+`, a.id `+order+` limit $7`,
		append(args, afterID, afterTime, f.Limit+1)...)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[Approval])
	if err != nil {
		return Page[Approval]{}, 0, err
	}
	return paginate(list, f.Limit, func(a Approval) Cursor {
		t := a.CreatedAt
		if pendingOnly && a.OpenedAt != nil {
			t = *a.OpenedAt
		} else if !pendingOnly && a.DecidedAt != nil {
			t = *a.DecidedAt
		}
		return Cursor{Value: t.Format(time.RFC3339Nano), ID: a.ID}
	}), total, nil
}

// OpenApproval makes a waiting level pending and starts its timeout. It
// reports whether it did: a level already opened (an activity retry) or
// decided is returned as it is.
func (s *Store) OpenApproval(ctx context.Context, db DB, id string) (Approval, bool, error) {
	rows, _ := db.Query(ctx, `update approvals a set status = 'pending', opened_at = now(),
		due_at = case when timeout_seconds is null then null else now() + make_interval(secs => timeout_seconds) end
		where a.id = $1 and a.status = 'waiting' returning `+approvalCols, id)
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Approval])
	if errors.Is(err, pgx.ErrNoRows) {
		a, err = s.GetApproval(ctx, db, id)
		return a, false, err
	}
	return a, err == nil, err
}

// FinishApproval decides a pending level. It reports false when the level
// was no longer pending (decided or timed out meanwhile).
func (s *Store) FinishApproval(ctx context.Context, db DB, id, status, outcome string) (bool, error) {
	tag, err := db.Exec(ctx, `update approvals set status = $2, outcome = $3, decided_at = now(), due_at = null
		where id = $1 and status = 'pending'`, id, status, outcome)
	return tag.RowsAffected() == 1, err
}

// EscalateApproval adds the escalation approvers and restarts the timeout.
func (s *Store) EscalateApproval(ctx context.Context, db DB, id string) (Approval, error) {
	rows, _ := db.Query(ctx, `update approvals a set escalated = true,
		due_at = case when timeout_seconds is null then null else now() + make_interval(secs => timeout_seconds) end
		where a.id = $1 and a.status = 'pending' returning `+approvalCols, id)
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Approval])
	return a, notFound(err)
}

// CancelApprovals ends the open levels of a cancelled request.
func (s *Store) CancelApprovals(ctx context.Context, db DB, requestID string) error {
	_, err := db.Exec(ctx, `update approvals set status = 'cancelled', due_at = null
		where request_id = $1 and status in ('waiting', 'pending')`, requestID)
	return err
}

func (s *Store) InsertVote(ctx context.Context, db DB, v ApprovalVote) error {
	_, err := db.Exec(ctx, `insert into approval_votes (approval_id, user_id, decision, comment, changes)
		values ($1, $2, $3, $4, $5)`, v.ApprovalID, v.UserID, v.Decision, v.Comment, jsonOr(v.Changes, "[]"))
	return err
}

// Votes returns the votes on the given levels, oldest first.
func (s *Store) Votes(ctx context.Context, db DB, approvalIDs []string) (map[string][]ApprovalVote, error) {
	rows, _ := db.Query(ctx, `select v.approval_id, v.user_id, u.name as user_name, u.email as user_email,
		v.decision, v.comment, v.changes, v.decided_at
		from approval_votes v join users u on u.id = v.user_id
		where v.approval_id = any($1) order by v.decided_at`, approvalIDs)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[ApprovalVote])
	out := map[string][]ApprovalVote{}
	for _, v := range list {
		out[v.ApprovalID] = append(out[v.ApprovalID], v)
	}
	return out, err
}

func (s *Store) InsertDelegate(ctx context.Context, db DB, d ApprovalDelegate) error {
	_, err := db.Exec(ctx, `insert into approval_delegates (approval_id, user_id, delegated_by, comment)
		values ($1, $2, $3, $4) on conflict (approval_id, user_id) do nothing`, d.ApprovalID, d.UserID, d.DelegatedBy, d.Comment)
	return err
}

func (s *Store) Delegates(ctx context.Context, approvalIDs []string) (map[string][]ApprovalDelegate, error) {
	rows, _ := s.Pool.Query(ctx, `select d.approval_id, d.user_id, u.name as user_name, u.email as user_email,
		d.delegated_by, d.comment, d.created_at
		from approval_delegates d join users u on u.id = d.user_id
		where d.approval_id = any($1) order by d.created_at`, approvalIDs)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[ApprovalDelegate])
	out := map[string][]ApprovalDelegate{}
	for _, d := range list {
		out[d.ApprovalID] = append(out[d.ApprovalID], d)
	}
	return out, err
}

// SetUserGroups records the groups from a user's latest token.
func (s *Store) SetUserGroups(ctx context.Context, userID string, groups []string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from user_groups where user_id = $1 and not (group_name = any($2))`,
			userID, nonNil(groups)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into user_groups (user_id, group_name)
			select $1, g from unnest($2::text[]) g
			on conflict (user_id, group_name) do update set seen_at = now()`, userID, nonNil(groups))
		return err
	})
}

func (s *Store) UserGroups(ctx context.Context, userID string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select group_name from user_groups where user_id = $1 order by group_name`, userID)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// GroupMembers returns the IDs of users known to be in any of the groups.
func (s *Store) GroupMembers(ctx context.Context, groups []string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select distinct user_id from user_groups where group_name = any($1) order by user_id`,
		nonNil(groups))
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// AuditEvent is one append-only audit record.
type AuditEvent struct {
	ActorID    *string
	Action     string
	TargetType string
	TargetID   string
	ProjectID  *string
	Detail     any
}

func (s *Store) Audit(ctx context.Context, db DB, e AuditEvent) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil || e.Detail == nil {
		detail = []byte("{}")
	}
	_, err = db.Exec(ctx, `insert into audit_events (actor_id, action, target_type, target_id, project_id, detail)
		values ($1, $2, $3, $4, $5, $6)`, e.ActorID, e.Action, e.TargetType, e.TargetID, e.ProjectID, detail)
	return err
}

// IsRequestApprover reports whether the user may decide one of the
// request's levels (named, in a group, escalated to or delegated).
func (s *Store) IsRequestApprover(ctx context.Context, requestID, userID string, groups []string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from approvals a where a.request_id = $1 and a.status <> 'cancelled'
		and ($2 = any(a.approver_ids) or a.group_name = any($3)
			or (a.escalated and ($2 = any(a.escalate_ids) or a.escalate_group = any($3)))
			or exists (select 1 from approval_delegates d where d.approval_id = a.id and d.user_id = $2)))`,
		requestID, userID, nonNil(groups)).Scan(&ok)
	return ok, err
}
