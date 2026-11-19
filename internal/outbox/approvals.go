package outbox

import (
	"context"
	"time"

	"github.com/damhau/rvav/internal/store"
)

// ForApproval writes the topic's event about one approval level of a
// request (ADR-0049): what an event says about the request, with the level
// in `approval`. decidedBy is the user who decided it, "" when rvav did (a
// timeout, or nobody able to). One event per level and topic, and one more
// approval.requested once it is escalated.
func ForApproval(ctx context.Context, db store.DB, topic string, lv store.Approval, decidedBy, comment string) error {
	data, deploymentID, err := requestData(ctx, db, lv.RequestID)
	if err != nil {
		return err
	}
	approvers, err := people(ctx, db, lv.NamedApprovers())
	if err != nil {
		return err
	}
	kind := "policy"
	if lv.WorkflowID != nil {
		kind = "workflow"
	}
	approval := map[string]any{"id": lv.ID, "name": lv.Name, "level": lv.Seq, "kind": kind, "mode": lv.Mode,
		"reasons": nonNil(lv.Reasons), "roles": nonNil(lv.Roles), "manager": lv.Manager, "groups": nonNil(lv.Groups()),
		"approvers": approvers, "escalated": lv.Escalated, "status": lv.Status, "due_at": lv.DueAt,
		"decided_at": lv.DecidedAt, "outcome": lv.Outcome, "decided_by": nil, "comment": nil}
	if decidedBy != "" {
		by, err := people(ctx, db, []string{decidedBy})
		if err != nil {
			return err
		}
		if len(by) > 0 {
			approval["decided_by"] = by[0]
		}
	}
	if comment != "" {
		approval["comment"] = comment
	}
	data["approval"] = approval
	dedupe := topic + ":" + lv.ID
	if topic == ApprovalRequested && lv.Escalated {
		dedupe += ":escalated"
	}
	return insert(ctx, db, topic, &lv.RequestID, deploymentID, dedupe, data, false)
}

// people are these users as events name them, by name.
func people(ctx context.Context, db store.DB, userIDs []string) ([]map[string]any, error) {
	rows, err := db.Query(ctx, `select jsonb_build_object('id', id, 'name', name, 'email', email) from users
		where id = any($1) order by name, id`, nonNil(userIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, raw(b))
	}
	return out, rows.Err()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func sampleApproval(topic string) map[string]any {
	due := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	a := map[string]any{"id": "apr_sample", "name": "Production change", "level": 1, "kind": "policy", "mode": "any",
		"reasons": []any{"Production environment"}, "roles": []any{}, "manager": false, "groups": []any{"rvav-approvers-web"},
		"approvers": []any{map[string]any{"id": "usr_approver", "name": "Sample Approver", "email": "approver@example.com"}},
		"escalated": false, "status": "pending", "due_at": due, "decided_at": nil, "outcome": nil, "decided_by": nil, "comment": nil}
	if topic == ApprovalDecided {
		a["status"], a["due_at"], a["decided_at"] = "approved", nil, time.Now().UTC().Format(time.RFC3339)
		a["outcome"], a["comment"] = "Approved by Sample Approver", "Fine for this sprint."
		a["decided_by"] = map[string]any{"id": "usr_approver", "name": "Sample Approver", "email": "approver@example.com"}
	}
	return a
}
