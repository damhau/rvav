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

// A VM's own topics (ADR-0049): one event per VM, named as vRA's.
const (
	ComputeAllocationPre = "compute.allocation.pre"
	NetworkConfigure     = "network.configure"
	ComputeProvisionPre  = "compute.provision.pre"
	ComputeProvisionPost = "compute.provision.post"
	ComputeRemovalPre    = "compute.removal.pre"
	ComputeRemovalPost   = "compute.removal.post"
)

// A request's approval levels, and projects (ADR-0049).
const (
	ApprovalRequested = "approval.requested"
	ApprovalDecided   = "approval.decided"
	ProjectCreated    = "project.created"
	ProjectUpdated    = "project.updated"
)

// A custom resource's (ADR-0056): one event per resource, as it is made
// and as it is deleted.
const (
	CustomCreatePre  = "custom.create.pre"
	CustomCreatePost = "custom.create.post"
	CustomDeletePre  = "custom.delete.pre"
	CustomDeletePost = "custom.delete.post"
)

// Topics lists them in the order of a deployment's life, then projects'.
var Topics = []string{RequestSubmitted, ApprovalRequested, ApprovalDecided, ProvisioningPre, ComputeAllocationPre, NetworkConfigure,
	ComputeProvisionPre, ComputeProvisionPost, CustomCreatePre, CustomCreatePost, ProvisioningPost, Day2Pre, Day2Post,
	DeletePre, CustomDeletePre, CustomDeletePost, ComputeRemovalPre, ComputeRemovalPost, DeletePost, LeaseExpiring, LeaseExpired,
	ProjectCreated, ProjectUpdated}

// Group says what a topic's events are about: a request, one of its VMs
// (data.machine), one of its custom resources (data.custom_resource), one
// of its approval levels (data.approval), a deployment's lease, or a
// project.
func Group(topic string) string {
	switch topic {
	case ComputeAllocationPre, NetworkConfigure, ComputeProvisionPre, ComputeProvisionPost, ComputeRemovalPre, ComputeRemovalPost:
		return "machine"
	case CustomCreatePre, CustomCreatePost, CustomDeletePre, CustomDeletePost:
		return "custom"
	case ApprovalRequested, ApprovalDecided:
		return "approval"
	case LeaseExpiring, LeaseExpired:
		return "lease"
	case ProjectCreated, ProjectUpdated:
		return "project"
	}
	return "request"
}

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
	case kind == "day2", kind == "update":
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
	return insert(ctx, db, topic, &requestID, deploymentID, dedupe, data, false)
}

// Submitted writes a request's request.submitted event, held until its
// workflow has started (store.ReleaseRequestEvents): a request Temporal
// refuses is removed, and subscribers never hear of it (#81).
func Submitted(ctx context.Context, db store.DB, requestID string) error {
	data, deploymentID, err := requestData(ctx, db, requestID)
	if err != nil {
		return err
	}
	return insert(ctx, db, RequestSubmitted, &requestID, deploymentID, requestID+":"+RequestSubmitted, data, true)
}

// ForMachine writes the topic's event about one VM of a request
// (ADR-0049): what an event says about the request, with the VM in
// `machine` and its deployment in `deployment`.
func ForMachine(ctx context.Context, db store.DB, topic, requestID, dedupe string, machine map[string]any) error {
	data, deploymentID, err := requestData(ctx, db, requestID)
	if err != nil {
		return err
	}
	data["machine"] = machine
	if id, _ := machine["deployment_id"].(string); id != "" {
		deploymentID = &id
		for _, d := range data["deployments"].([]map[string]any) {
			if d["id"] == id {
				data["deployment"] = d
			}
		}
	}
	return insert(ctx, db, topic, &requestID, deploymentID, dedupe, data, false)
}

// ForCustom writes the topic's event about one custom resource of a
// request (ADR-0056): what an event says about the request, with the
// resource in `custom_resource` and its deployment in `deployment`.
func ForCustom(ctx context.Context, db store.DB, topic, requestID, dedupe string, resource map[string]any) error {
	data, deploymentID, err := requestData(ctx, db, requestID)
	if err != nil {
		return err
	}
	data["custom_resource"] = resource
	if id, _ := resource["deployment_id"].(string); id != "" {
		deploymentID = &id
		for _, d := range data["deployments"].([]map[string]any) {
			if d["id"] == id {
				data["deployment"] = d
			}
		}
	}
	return insert(ctx, db, topic, &requestID, deploymentID, dedupe, data, false)
}

// ForDeployment writes the topic's event about a deployment without a
// request (its lease reminder or expiry).
func ForDeployment(ctx context.Context, db store.DB, topic, deploymentID, dedupe string) error {
	data, err := deploymentData(ctx, db, deploymentID)
	if err != nil {
		return err
	}
	return insert(ctx, db, topic, nil, &deploymentID, dedupe, data, false)
}

func insert(ctx context.Context, db store.DB, topic string, requestID, deploymentID *string, dedupe string, data map[string]any, held bool) error {
	ev := Event{ID: ids.New(ids.Event), Topic: topic, Contract: Contract, OccurredAt: time.Now().UTC(), Data: data}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `insert into integration_events (id, topic, request_id, deployment_id, dedupe_key, payload, held)
		values ($1, $2, $3, $4, $5, $6, $7) on conflict (dedupe_key) do nothing`, ev.ID, topic, requestID, deploymentID, dedupe, b, held)
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
	case DeletePre, ComputeRemovalPre, ComputeRemovalPost, CustomDeletePre, CustomDeletePost:
		data["request"].(map[string]any)["kind"], data["request"].(map[string]any)["action"] = "day2", "delete"
	case LeaseExpiring, LeaseExpired:
		data["request"] = nil
	}
	switch Group(topic) {
	case "machine":
		data["machine"] = sampleMachine(topic)
		if topic == ComputeAllocationPre || topic == NetworkConfigure || topic == ComputeProvisionPre {
			data["resources"] = []any{}
		}
	case "custom":
		data["custom_resource"] = sampleCustom(topic)
	case "approval":
		data["request"].(map[string]any)["status"] = "pending_approval"
		data["approval"] = sampleApproval(topic)
		data["deployment"], data["deployments"], data["resources"] = nil, []any{}, []any{}
	case "project":
		data = sampleProject(topic)
	}
	return Event{ID: "evt_sample", Topic: topic, Contract: Contract, OccurredAt: time.Now().UTC(), Data: data}
}

// sampleCustom is a made-up custom resource as the topic's event has it:
// no properties before it is made.
func sampleCustom(topic string) map[string]any {
	r := map[string]any{"id": "res_sample_lb", "type": map[string]any{"key": "load-balancer", "title": "Load balancer"},
		"key": "lb", "name": "web-dev-app-lb", "deployment_id": "dep_sample", "phase": "create",
		"inputs": map[string]any{"members": []any{"10.31.10.57"}, "port": 443}, "properties": map[string]any{"vip": "10.31.20.10"}}
	switch topic {
	case CustomCreatePre:
		r["properties"] = map[string]any{}
	case CustomDeletePre, CustomDeletePost:
		r["phase"] = "delete"
	}
	return r
}

// sampleMachine is a made-up VM as the topic's event has it: no address
// before compute.provision.pre, no moref before compute.provision.post.
func sampleMachine(topic string) map[string]any {
	nic := map[string]any{"index": 0, "key": "", "network": "pg-web-dev", "segment": "", "ipam": "efficientip",
		"subnet": "10.31.10.0/24", "gateway": "10.31.10.1", "prefix_length": 24, "subnet6": "", "dns_zone": "corp.example",
		"fqdn": "web-dev-app001.corp.example", "address": nil}
	m := map[string]any{"name": "web-dev-app001", "fqdn": "web-dev-app001.corp.example", "component": "app", "index": 0,
		"deployment_id": "dep_sample", "resource_id": nil, "moref": nil, "address": nil,
		"template": "rhel9-template", "vcpu": 4, "memory_gb": 16, "disk_gb": 80,
		"placement": map[string]any{"vcenter": "vc-gva-01", "cluster": "cl-gva-dev-01", "resource_pool": "", "hosts": []any{},
			"datastore": "ds-gva-dev-01", "datastore_cluster": "", "folder": "rvav/web"},
		"networks": []any{nic}, "tags": []any{"env/dev"}, "properties": map[string]any{"cost_center": "CC-0000"},
	}
	if topic == ComputeAllocationPre || topic == NetworkConfigure {
		return m
	}
	m["address"], nic["address"] = "10.31.10.57", "10.31.10.57"
	if topic != ComputeProvisionPre {
		m["resource_id"], m["moref"] = "res_sample", "vm-1042"
	}
	return m
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
		inputs, uiSchema, bp, plan      []byte
		item, project, requester, owner []byte
		environment                     string
		production                      bool
	)
	err := db.QueryRow(ctx, `select r.id, r.number, r.kind, r.action, r.status, r.deployment_id, r.resource_id, r.resource_name,
			r.inputs, v.ui_schema, v.blueprint, r.plan,
			jsonb_build_object('id', i.id, 'slug', i.slug, 'name', i.name, 'type', i.type, 'version', v.number),
			jsonb_build_object('id', p.id, 'code', p.code, 'name', p.name, 'cost_center', p.cost_center),
			jsonb_build_object('id', ru.id, 'name', ru.name, 'email', ru.email),
			jsonb_build_object('id', ou.id, 'name', ou.name, 'email', ou.email),
			e.key, e.production
		from requests r join catalog_items i on i.id = r.item_id join catalog_item_versions v on v.id = r.version_id
		join projects p on p.id = r.project_id join environments e on e.key = r.environment
		join users ru on ru.id = r.requester_id join users ou on ou.id = r.owner_id
		where r.id = $1`, requestID).Scan(&id, &number, &kind, &action, &status, &deploymentID, &resourceID, &resourceName,
		&inputs, &uiSchema, &bp, &plan,
		&item, &project, &requester, &owner, &environment, &production)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, store.ErrNotFound
	} else if err != nil {
		return nil, nil, err
	}
	if uiSchema, err = store.UISchemaWithGroups(ctx, db, uiSchema, bp); err != nil {
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
		inputs, uiSchema, bp []byte
	)
	err := db.QueryRow(ctx, `select jsonb_build_object('id', i.id, 'slug', i.slug, 'name', i.name, 'type', i.type, 'version', v.number),
			jsonb_build_object('id', p.id, 'code', p.code, 'name', p.name, 'cost_center', p.cost_center),
			jsonb_build_object('id', ou.id, 'name', ou.name, 'email', ou.email), e.key, e.production, d.inputs, v.ui_schema, v.blueprint
		from deployments d join catalog_items i on i.id = d.item_id join catalog_item_versions v on v.id = d.version_id
		join projects p on p.id = d.project_id join environments e on e.key = d.environment join users ou on ou.id = d.owner_id
		where d.id = $1`, deploymentID).Scan(&item, &project, &owner, &environment, &production, &inputs, &uiSchema, &bp)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if uiSchema, err = store.UISchemaWithGroups(ctx, db, uiSchema, bp); err != nil {
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
				and a.type = 'ipam.address' and a.properties->>'hostname' = r.name and strpos(a.name, ':') = 0
				order by a.created_at limit 1) end))
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

// publicInputs leaves out the inputs of secret fields, the item's and its
// property groups', as the API does.
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
