// Package outbox writes integration events (ADR-0021) in the transaction of
// the change they describe. The dispatcher (package dispatch) delivers them
// to the subscriptions of their topic.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Contract is the version of the event envelope.
const Contract = "rvav.event.v1"

// Topics.
const (
	RequestSubmitted = "request.submitted"
	ProvisioningPre  = "provisioning.pre"
	ProvisioningPost = "provisioning.post"
	Day2Pre          = "day2.pre"
	Day2Post         = "day2.post"
	DeletePre        = "delete.pre"
	DeletePost       = "delete.post"
	LeaseExpiring    = "lease.expiring"
	LeaseExpired     = "lease.expired"
)

// Topics lists them in the order of a deployment's life.
var Topics = []string{RequestSubmitted, ProvisioningPre, ProvisioningPost, Day2Pre, Day2Post,
	DeletePre, DeletePost, LeaseExpiring, LeaseExpired}

// MayBlock says whether blocking subscriptions are allowed on a topic.
func MayBlock(topic string) bool {
	return topic == ProvisioningPre || topic == Day2Pre || topic == DeletePre
}

// Event is what subscribers receive.
type Event struct {
	ID         string         `json:"id"`
	Topic      string         `json:"topic"`
	Contract   string         `json:"contract"`
	OccurredAt time.Time      `json:"occurred_at"`
	Data       map[string]any `json:"data"`
}

// RequestTopics are the pre and post topics of a request: a deletion's
// rather than day-2's when the action deletes the deployment.
func RequestTopics(kind, action string) (pre, post string) {
	switch {
	case kind == "day2" && action == "delete":
		return DeletePre, DeletePost
	case kind == "day2":
		return Day2Pre, Day2Post
	}
	return ProvisioningPre, ProvisioningPost
}

// ForRequest writes the topic's event about a request, read through db (a
// transaction sees what it wrote). The dedupe key makes a second write of
// the same event, by a retried activity, do nothing. A post event has the
// request's outcome: succeeded, failed or cancelled.
func ForRequest(ctx context.Context, db store.DB, topic, requestID, dedupe, outcome string) error {
	data, deploymentID, err := requestData(ctx, db, requestID)
	if err != nil {
		return err
	}
	if outcome != "" {
		data["request"].(map[string]any)["outcome"] = outcome
	}
	return insert(ctx, db, topic, &requestID, deploymentID, dedupe, data)
}

// ForDeployment writes the topic's event about a deployment without a
// request (its lease reminder or expiry).
func ForDeployment(ctx context.Context, db store.DB, topic, deploymentID, dedupe string) error {
	data, err := deploymentData(ctx, db, deploymentID)
	if err != nil {
		return err
	}
	return insert(ctx, db, topic, nil, &deploymentID, dedupe, data)
}

func insert(ctx context.Context, db store.DB, topic string, requestID, deploymentID *string, dedupe string, data map[string]any) error {
	ev := Event{ID: ids.New(ids.Event), Topic: topic, Contract: Contract, OccurredAt: time.Now().UTC(), Data: data}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `insert into integration_events (id, topic, request_id, deployment_id, dedupe_key, payload)
		values ($1, $2, $3, $4, $5, $6) on conflict (dedupe_key) do nothing`, ev.ID, topic, requestID, deploymentID, dedupe, b)
	return err
}

// Sample is a made-up event of the topic, for subscription tests and for
// checking conditions when they are saved.
func Sample(topic string) Event {
	dep := map[string]any{"id": "dep_sample", "name": "web-dev-app001", "status": "ready", "properties": map[string]any{},
		"lease_expires_at": time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)}
	data := map[string]any{
		"request": map[string]any{"id": "req_sample", "number": "REQ-00000", "kind": "provision", "action": nil,
			"status": "running", "outcome": nil, "resource_id": nil, "resource_name": nil},
		"item":        map[string]any{"id": "itm_sample", "slug": "rhel9-server", "name": "RHEL 9 server", "type": "vm_blueprint", "version": "1.0"},
		"project":     map[string]any{"id": "prj_sample", "code": "web", "name": "Web Platform", "cost_center": "CC-0000"},
		"environment": "dev", "production": false,
		"requester":  map[string]any{"id": "usr_sample", "name": "Sample Requester", "email": "requester@example.com"},
		"owner":      map[string]any{"id": "usr_sample", "name": "Sample Requester", "email": "requester@example.com"},
		"deployment": dep, "deployments": []any{dep},
		"resources": []any{map[string]any{"id": "res_sample", "type": "vsphere.vm", "name": "web-dev-app001",
			"fqdn": "web-dev-app001.corp.example", "ip": "10.31.10.57", "moref": "vm-1042", "deployment_id": "dep_sample"}},
		"inputs": map[string]any{"role": "app", "size": "M"},
		"vm":     map[string]any{"vcpu": 4, "memory_gb": 16, "disk_gb": 80, "count": 1},
	}
	switch topic {
	case ProvisioningPost, Day2Post, DeletePost:
		data["request"].(map[string]any)["outcome"] = "succeeded"
	case Day2Pre:
		data["request"].(map[string]any)["kind"], data["request"].(map[string]any)["action"] = "day2", "resize"
	case DeletePre:
		data["request"].(map[string]any)["kind"], data["request"].(map[string]any)["action"] = "day2", "delete"
	case LeaseExpiring, LeaseExpired:
		data["request"] = nil
	}
	return Event{ID: "evt_sample", Topic: topic, Contract: Contract, OccurredAt: time.Now().UTC(), Data: data}
}

// Vars are what a subscription's condition sees: the event's data, with
// `environment` as its key and `production` beside it.
func Vars(data map[string]any) map[string]any {
	vars := make(map[string]any, len(data)+2)
	for k, v := range data {
		vars[k] = v
	}
	if _, ok := vars["production"]; !ok {
		vars["production"] = false
	}
	return vars
}

// RequestData is what an event says about a request (a custom workflow's
// context has the same, ADR-0022).
func RequestData(ctx context.Context, db store.DB, requestID string) (map[string]any, error) {
	data, _, err := requestData(ctx, db, requestID)
	return data, err
}

// requestData reads what an event says about a request.
func requestData(ctx context.Context, db store.DB, requestID string) (map[string]any, *string, error) {
	var (
		id, number, kind, status        string
		action, deploymentID            *string
		resourceID, resourceName        *string
		inputs, uiSchema, plan          []byte
		item, project, requester, owner []byte
		environment                     string
		production                      bool
	)
	err := db.QueryRow(ctx, `select r.id, r.number, r.kind, r.action, r.status, r.deployment_id, r.resource_id, r.resource_name,
			r.inputs, v.ui_schema, r.plan,
			jsonb_build_object('id', i.id, 'slug', i.slug, 'name', i.name, 'type', i.type, 'version', v.number),
			jsonb_build_object('id', p.id, 'code', p.code, 'name', p.name, 'cost_center', p.cost_center),
			jsonb_build_object('id', ru.id, 'name', ru.name, 'email', ru.email),
			jsonb_build_object('id', ou.id, 'name', ou.name, 'email', ou.email),
			e.key, e.production
		from requests r join catalog_items i on i.id = r.item_id join catalog_item_versions v on v.id = r.version_id
		join projects p on p.id = r.project_id join environments e on e.key = r.environment
		join users ru on ru.id = r.requester_id join users ou on ou.id = r.owner_id
		where r.id = $1`, requestID).Scan(&id, &number, &kind, &action, &status, &deploymentID, &resourceID, &resourceName,
		&inputs, &uiSchema, &plan,
		&item, &project, &requester, &owner, &environment, &production)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, store.ErrNotFound
	} else if err != nil {
		return nil, nil, err
	}
	data := map[string]any{
		"request": map[string]any{"id": id, "number": number, "kind": kind, "action": action, "status": status, "outcome": nil,
			"resource_id": resourceID, "resource_name": resourceName},
		"environment": environment, "production": production,
		"item": raw(item), "project": raw(project), "requester": raw(requester), "owner": raw(owner),
		"inputs": publicInputs(inputs, uiSchema), "vm": vmVars(plan),
	}
	// A day-2 request's deployment, or those a catalog request makes.
	where, arg := "d.created_by_request_id = $1", requestID
	if deploymentID != nil {
		where, arg = "d.id = $1", *deploymentID
	}
	deps, resources, err := deployments(ctx, db, where, arg)
	if err != nil {
		return nil, nil, err
	}
	data["deployments"], data["resources"] = deps, resources
	data["deployment"] = nil
	if len(deps) > 0 {
		data["deployment"] = deps[0]
		if deploymentID == nil {
			deploymentID = present.Ptr(deps[0]["id"].(string))
		}
	}
	return data, deploymentID, nil
}

// deploymentData reads what an event says about a deployment alone.
func deploymentData(ctx context.Context, db store.DB, deploymentID string) (map[string]any, error) {
	var (
		item, project, owner []byte
		environment          string
		production           bool
		inputs, uiSchema     []byte
	)
	err := db.QueryRow(ctx, `select jsonb_build_object('id', i.id, 'slug', i.slug, 'name', i.name, 'type', i.type, 'version', v.number),
			jsonb_build_object('id', p.id, 'code', p.code, 'name', p.name, 'cost_center', p.cost_center),
			jsonb_build_object('id', ou.id, 'name', ou.name, 'email', ou.email), e.key, e.production, d.inputs, v.ui_schema
		from deployments d join catalog_items i on i.id = d.item_id join catalog_item_versions v on v.id = d.version_id
		join projects p on p.id = d.project_id join environments e on e.key = d.environment join users ou on ou.id = d.owner_id
		where d.id = $1`, deploymentID).Scan(&item, &project, &owner, &environment, &production, &inputs, &uiSchema)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	deps, resources, err := deployments(ctx, db, "d.id = $1", deploymentID)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"request": nil, "environment": environment, "production": production,
		"item": raw(item), "project": raw(project), "requester": nil, "owner": raw(owner),
		"inputs": publicInputs(inputs, uiSchema), "vm": nil, "deployments": deps, "resources": resources, "deployment": nil}
	if len(deps) > 0 {
		data["deployment"] = deps[0]
	}
	return data, nil
}

// deployments reads deployments and their resources (VMs with their
// address, DNS records and addresses).
func deployments(ctx context.Context, db store.DB, where, arg string) ([]map[string]any, []map[string]any, error) {
	rows, err := db.Query(ctx, `select jsonb_build_object('id', d.id, 'name', d.name, 'status', d.status, 'properties', d.properties,
			'lease_expires_at', d.lease_expires_at), d.id
		from deployments d where `+where+` order by d.created_at, d.id`, arg)
	if err != nil {
		return nil, nil, err
	}
	var deps []map[string]any
	var depIDs []string
	for rows.Next() {
		var b []byte
		var id string
		if err := rows.Scan(&b, &id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		deps, depIDs = append(deps, raw(b)), append(depIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows, err = db.Query(ctx, `select jsonb_strip_nulls(jsonb_build_object('id', r.id, 'type', r.type, 'name', r.name,
			'deployment_id', r.deployment_id, 'moref', r.properties#>>'{vm,moref}',
			'fqdn', coalesce(r.properties#>>'{vm,fqdn}', r.properties->>'fqdn'),
			'ip', case when r.type = 'vsphere.vm' then (select a.name from resources a where a.deployment_id = r.deployment_id
				and a.type = 'ipam.address' and a.properties->>'hostname' = r.name order by a.created_at limit 1) end))
		from resources r where r.deployment_id = any($1) order by r.created_at, r.id`, depIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	resources := []map[string]any{}
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, nil, err
		}
		resources = append(resources, raw(b))
	}
	if deps == nil {
		deps = []map[string]any{}
	}
	return deps, resources, rows.Err()
}

func raw(b []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// publicInputs leaves out the inputs of secret fields, as the API does.
func publicInputs(inputs, uiSchema []byte) map[string]any {
	m := raw(inputs)
	if m == nil {
		m = map[string]any{}
	}
	for k := range present.SecretFields(uiSchema) {
		delete(m, k)
	}
	return m
}

// vmVars are the size of a request's first VM and how many it builds.
func vmVars(plan []byte) map[string]any {
	var p struct {
		VMs []struct {
			VCPU     int `json:"vcpu"`
			MemoryGB int `json:"memory_gb"`
			DiskGB   int `json:"disk_gb"`
		} `json:"vms"`
	}
	_ = json.Unmarshal(plan, &p)
	if len(p.VMs) == 0 {
		return nil
	}
	return map[string]any{"vcpu": p.VMs[0].VCPU, "memory_gb": p.VMs[0].MemoryGB, "disk_gb": p.VMs[0].DiskGB, "count": len(p.VMs)}
}
