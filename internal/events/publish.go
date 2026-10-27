// Package events writes live events to the outbox (Publisher) and fans them
// out to Server-Sent Events subscribers (Hub).
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Publisher records events after the change they describe has been committed.
// Failures are logged, not returned: a missed event costs a client refetch,
// never a failed request.
type Publisher struct {
	Store  *store.Store
	Logger *slog.Logger

	secrets sync.Map // version ID -> map[string]bool
}

// payload is what the outbox stores; the hub adds the event ID when sending.
type payload struct {
	Type string    `json:"type"`
	At   time.Time `json:"at"`
	Data any       `json:"data"`
}

func (p *Publisher) insert(ctx context.Context, typ string, projectID *string, topics []string, data any) {
	_, err := p.Store.InsertEvent(ctx, p.Store.Pool, typ, projectID, topics, payload{Type: typ, At: time.Now().UTC(), Data: data})
	if err != nil {
		p.Logger.Warn("publish event", "type", typ, "err", err)
	}
}

func (p *Publisher) secretFields(ctx context.Context, versionID string) map[string]bool {
	if v, ok := p.secrets.Load(versionID); ok {
		return v.(map[string]bool)
	}
	ver, err := p.Store.GetVersion(ctx, versionID)
	if err != nil {
		return map[string]bool{}
	}
	m := present.SecretFields(ver.UISchema)
	p.secrets.Store(versionID, m)
	return m
}

func requestTopics(id string) []string { return []string{"requests", "request:" + id} }

// Request publishes request.updated with the request's current state.
func (p *Publisher) Request(ctx context.Context, requestID string) {
	r, err := p.Store.GetRequest(ctx, requestID)
	if err != nil {
		p.Logger.Warn("publish request", "request", requestID, "err", err)
		return
	}
	p.insert(ctx, "request.updated", &r.ProjectID, requestTopics(r.ID), present.Request(r, p.secretFields(ctx, r.VersionID)))
}

// Step publishes request.step for one step (without its children).
func (p *Publisher) Step(ctx context.Context, projectID, requestID string, s store.Step) {
	p.insert(ctx, "request.step", &projectID, requestTopics(requestID), map[string]any{
		"request_id":     requestID,
		"parent_step_id": s.ParentID,
		"step":           present.Step(s),
	})
}

// Log publishes request.log: new lines exist up to nextOffset.
func (p *Publisher) Log(ctx context.Context, projectID, requestID, stepID string, nextOffset int) {
	p.insert(ctx, "request.log", &projectID, requestTopics(requestID), map[string]any{
		"request_id": requestID, "step_id": stepID, "next_offset": nextOffset,
	})
}

// Deployment publishes deployment.updated.
func (p *Publisher) Deployment(ctx context.Context, deploymentID string) {
	d, err := p.Store.GetDeployment(ctx, deploymentID)
	if err != nil {
		p.Logger.Warn("publish deployment", "deployment", deploymentID, "err", err)
		return
	}
	p.insert(ctx, "deployment.updated", &d.ProjectID, []string{"deployments", "deployment:" + d.ID}, present.Deployment(d))
}

// Resource publishes resource.updated.
func (p *Publisher) Resource(ctx context.Context, projectID string, r store.Resource) {
	res, ok := present.Resource(r)
	if !ok {
		return
	}
	p.insert(ctx, "resource.updated", &projectID, []string{"deployments", "deployment:" + r.DeploymentID},
		map[string]any{"deployment_id": r.DeploymentID, "resource": res})
}

// Notify sends a notification to one user.
func (p *Publisher) Notify(ctx context.Context, userID, title, body, level, link string) {
	data := map[string]any{"id": time.Now().UTC().Format("20060102150405.000000"), "title": title, "level": level}
	if body != "" {
		data["body"] = body
	}
	if link != "" {
		data["link"] = link
	}
	p.insert(ctx, "notification", nil, []string{"notifications", "user:" + userID}, data)
}

// Approval tells each of the given users that an approval they may decide
// changed. Only IDs are sent: clients refetch with their own permissions.
func (p *Publisher) Approval(ctx context.Context, userIDs []string, approvalID, requestID, status string) {
	if len(userIDs) == 0 {
		return
	}
	topics := []string{"approvals"}
	for _, id := range userIDs {
		topics = append(topics, "user:"+id)
	}
	p.insert(ctx, "approval.updated", nil, topics, map[string]any{"approval_id": approvalID, "request_id": requestID, "status": status})
}

// Encode returns the JSON for an event as sent to clients, with its ID set.
func Encode(e store.Event) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(e.Payload, &m); err != nil {
		return nil, err
	}
	m["id"] = formatID(e.ID)
	return json.Marshal(m)
}
