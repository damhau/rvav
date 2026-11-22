package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/provision"
)

// aliasAgentForm is a form designed for the monitoring agent's AWX action,
// offered on VMs and on DNS aliases: what the agent watches is, by default,
// the alias's FQDN, which its type's create gave, or the VM's.
const aliasAgentForm = `{
	"form_schema": {"type": "object", "required": ["monitoring_agent", "listen_port", "watch"], "properties": {
		"monitoring_agent": {"type": "string", "enum": ["node_exporter", "telegraf"]},
		"listen_port": {"type": "integer", "minimum": 1024, "maximum": 65535},
		"watch": {"type": "string"}}},
	"ui_schema": {
		"layout": {"kind": "page", "sections": [{"key": "agent", "title": "Agent", "fields": ["monitoring_agent", "listen_port", "watch"]}]},
		"fields": {
			"monitoring_agent": {"widget": "select", "label": "Agent", "rules": {"default": "'telegraf'"}},
			"listen_port": {"widget": "number", "label": "Port", "rules": {"default": "8125"}},
			"watch": {"widget": "text", "label": "Watch",
				"rules": {"default": "has(resource.properties) ? resource.properties.fqdn : resource.fqdn"}}}},
	"initial_values": {}}`

// vmNetworks are the networks vCenter has the VM on.
func vmNetworks(t *testing.T, ctx context.Context, c *vim25.Client, name string) []string {
	t.Helper()
	vm, err := find.NewFinder(c, true).VirtualMachine(ctx, "/GVA/vm/rvav/web/dev/"+name)
	if err != nil {
		t.Fatal(err)
	}
	var m mo.VirtualMachine
	if err := property.DefaultCollector(c).RetrieveOne(ctx, vm.Reference(), []string{"network"}, &m); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ref := range m.Network {
		n, err := object.NewCommon(c, ref).ObjectName(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// findStep is the step of that key, at any depth.
func findStep(list []api.Step, key string) *api.Step {
	for i := range list {
		if list[i].Key == key {
			return &list[i]
		}
		if s := findStep(list[i].Children, key); s != nil {
			return s
		}
	}
	return nil
}

// TestE1Acceptance is milestone E1's acceptance (#166), through the API the
// pages use and a real Temporal dev server. Blocking subscriptions rename
// a VM and move it to another network before it is built. A custom
// resource type made by AWX templates lists a day-2 action whose designed
// form reads the resource; run on it, the job gets what the form worked
// out, and the type's read gives the resource new properties. A composite
// loops over the hosts a REST call lists: a pass fails, its catch path
// opens a ticket, and the request succeeds.
func TestE1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web, queue = "mkeller", "tnguyen", "prj_web", "rvav-e1-e2e"
		rig := askRig{t: t, h: h, ctx: ctx, admin: admin}
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		steps := func(requestID string) []api.Step {
			t.Helper()
			var list struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+requestID+"/steps", owner, nil, 200, &list)
			return list.Items
		}

		// The CMDB names the VM, the IPAM moves it to the database network.
		hook := &changeHook{answers: map[string]string{}, events: map[string]map[string]any{}}
		srv := httptest.NewServer(hook)
		defer srv.Close()
		h.allowWebhooks(srv.URL)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-signing-secret")}, 201, nil)
		var subs []api.Subscription
		for _, s := range []struct {
			name  string
			topic api.EventTopicKey
			path  string
		}{{"CMDB naming", api.ComputeAllocationPre, "/alloc"}, {"IPAM network", api.NetworkConfigure, "/net"}} {
			var target api.SubscriptionTarget
			_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+srv.URL+s.path+`", "secret": "cmdb-hook"}}`), &target)
			var sub api.Subscription
			h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: s.name, Topic: s.topic, Target: target,
				Blocking: new(true), TimeoutSeconds: new(5)}, 201, &sub)
			subs = append(subs, sub)
		}
		hook.answer("/alloc", `{"machine": {"name": "web-dev-app742"}}`)
		hook.answer("/net", `{"machine": {"networks": [{"index": 0, "network": "dvpg-db-dev-vl320"}]}}`)
		var server api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, owner, nil, 200, &server)
		vmReq := rig.submit(owner, api.RequestDraft{CatalogItemId: server.Id, VersionId: server.Version.Id, ProjectId: web,
			Environment: "dev", Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}})
		vmReq = h.waitStatus(vmReq.Id, owner, api.RequestStatusSucceeded)
		if got := hook.took(); !slices.Equal(got, []string{"/alloc web-dev-app001", "/net web-dev-app742"}) {
			t.Fatalf("the subscriptions were asked %v", got)
		}
		if n := findStep(steps(vmReq.Id), "checks:network.configure"); n == nil || len(n.Children) != 1 ||
			deref(n.Children[0].Detail) != "Went on, moving adapter 0 to dvpg-db-dev-vl320 (10.32.20.0/24)." {
			t.Fatalf("the network checks = %+v", n)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+(*vmReq.DeploymentIds)[0], owner, nil, 200, &dep)
		var vm api.VmResource
		var addresses []string
		for _, r := range dep.Resources {
			switch v, _ := r.ValueByDiscriminator(); v := v.(type) {
			case api.VmResource:
				vm = v
			case api.IpAddressResource:
				addresses = append(addresses, v.Address)
			}
		}
		if dep.Name != "web-dev-app742" || vm.Name != "web-dev-app742" || len(vm.Vm.Nics) != 1 || vm.Vm.Nics[0].Network != "dvpg-db-dev-vl320" {
			t.Fatalf("the deployment %s has the VM %+v", dep.Name, vm)
		}
		if got := vmNetworks(t, ctx, c, "web-dev-app742"); !slices.Equal(got, []string{"dvpg-db-dev-vl320"}) || vmExists(ctx, c, "web-dev-app001") {
			t.Fatalf("vCenter has web-dev-app742 on %v", got)
		}
		if len(addresses) != 1 || !netip.MustParsePrefix("10.32.20.0/24").Contains(netip.MustParseAddr(addresses[0])) {
			t.Fatalf("the VM's addresses = %v", addresses)
		}
		found, _, err := h.svc.IPAM.Find(ctx, addresses, nil)
		if err != nil || len(found) != 1 || found[0].Hostname != deref(vm.Vm.Fqdn) || !strings.HasPrefix(found[0].Hostname, "web-dev-app742.") {
			t.Fatalf("EfficientIP has %+v (%v), the VM is %s", found, err, deref(vm.Vm.Fqdn))
		}
		for _, s := range subs {
			h.json("DELETE", "/api/v1/admin/subscriptions/"+s.Id, admin, nil, 204, nil)
		}

		// A DNS alias type AWX templates make, read and delete, which lists
		// the monitoring agent's action; the action's form is designed.
		for _, tpl := range []string{"alias-create", "alias-read", "alias-delete"} {
			h.awx.Plays[tpl] = []string{"Gathering Facts", "dns : " + tpl}
		}
		h.awx.Artifacts = map[string]map[string]any{
			"alias-create": {"fqdn": "farm.apps.corp.example"},
			"alias-read":   {"fqdn": "farm.apps.corp.example", "ttl": 300},
		}
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log,
			Filter: awx.TemplateFilter{Labels: []string{"catalog"}}, ActionLabels: []string{"day2"}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", admin, nil, 200, nil)
		const agent = "awx.monitoring-agent"
		h.json("POST", "/api/v1/admin/resource-types", admin, api.ResourceTypeWrite{Key: new("dns-alias"), Title: "DNS alias",
			Create:  api.ResourceTypeDriver{Kind: api.ResourceDriverAwx, Name: "alias-create"},
			Read:    &api.ResourceTypeDriver{Kind: api.ResourceDriverAwx, Name: "alias-read"},
			Delete:  api.ResourceTypeDriver{Kind: api.ResourceDriverAwx, Name: "alias-delete"},
			Actions: &[]string{agent}}, 201, nil)
		var design api.Day2FormDesign
		h.json("PUT", "/api/v1/admin/day2-forms/"+agent, admin, api.Day2FormWrite{Definition: designedDefinition(t, aliasAgentForm, nil),
			BaseRevision: new(0), Active: new(true)}, 200, &design)
		if !design.Active {
			t.Fatalf("the designed form = %+v", design)
		}

		def := farmWithResources(map[string]any{"key": "alias", "type": "dns-alias", "with": map[string]any{"target": "components.web.fqdns[0]"}})
		var farm api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers with an alias.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &farm)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+farm.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		built := rig.submit(owner, api.RequestDraft{CatalogItemId: farm.Id, VersionId: farm.DraftVersion.Id, ProjectId: web, Environment: "dev",
			Inputs: map[string]any{"app": "farm", "web_nodes": 1, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}})
		built = h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]
		alias := customOf(h, owner, depID)["alias"]
		if alias.ResourceType.Key != "dns-alias" || alias.Properties["fqdn"] != "farm.apps.corp.example" {
			t.Fatalf("the alias = %+v", alias)
		}

		// The action is offered on the alias with the designed form, which
		// reads the alias's properties, and a VM's FQDN on a VM.
		var offered struct{ Items []api.Day2Action }
		h.json("GET", "/api/v1/deployments/"+depID+"/actions", owner, nil, 200, &offered)
		var onAlias, onVM *api.Day2Action
		for i, a := range offered.Items {
			switch {
			case a.Key != agent || a.ResourceId == nil:
			case *a.ResourceId == alias.Id:
				onAlias = &offered.Items[i]
			default:
				onVM = &offered.Items[i]
			}
		}
		if onAlias == nil || onVM == nil || onAlias.Form == nil || onAlias.Form.Designed == nil || !*onAlias.Form.Designed {
			t.Fatalf("the monitoring agent is offered on the alias as %+v, on a VM as %+v", onAlias, onVM)
		}
		preview := func(resourceID string) api.FieldStates {
			t.Helper()
			var p api.Day2FormPreview
			h.json("POST", "/api/v1/deployments/"+depID+"/actions/preview", owner, api.Day2FormQuery{Action: agent, ResourceId: &resourceID,
				Inputs: map[string]any{}}, 200, &p)
			if len(p.Errors) != 0 {
				t.Fatalf("the preview on %s refuses %+v", resourceID, p.Errors)
			}
			return p.Fields
		}
		if f := preview(alias.Id); f["watch"].Default != "farm.apps.corp.example" || f["listen_port"].Default != float64(8125) {
			t.Fatalf("the form on the alias = %+v", f)
		}
		if f := preview(*onVM.ResourceId); !strings.HasPrefix(fmt.Sprint(f["watch"].Default), deref(onVM.ResourceName)+".") {
			t.Fatalf("the form on %s = %+v", deref(onVM.ResourceName), f)
		}

		// Run on the alias, the job gets the form's defaults and the alias;
		// the type's read then gives the alias its TTL.
		var run api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", owner, api.Day2Draft{Action: agent, ResourceId: &alias.Id,
			Inputs: map[string]any{"listen_port": 9100}}, 202, &run, "Idempotency-Key", uuid.NewString())
		run = h.waitStatus(run.Id, owner, api.RequestStatusSucceeded)
		job, err := h.store.GetStepByKey(ctx, run.Id, "awx.job:monitoring-agent")
		if err != nil || job.ExternalID == nil {
			t.Fatalf("the job's step = %+v (%v)", job, err)
		}
		var jobID int
		fmt.Sscan(*job.ExternalID, &jobID)
		vars := h.awx.Launched(jobID).ExtraVars
		rvav, _ := vars["rvav"].(map[string]any)
		resource, _ := rvav["resource"].(map[string]any)
		if vars["monitoring_agent"] != "telegraf" || fmt.Sprint(vars["listen_port"]) != "9100" || vars["watch"] != "farm.apps.corp.example" ||
			resource["id"] != alias.Id || resource["phase"] != "day2" {
			t.Fatalf("the job's extra vars = %v", vars)
		}
		if got := customOf(h, owner, depID)["alias"]; got.Properties["ttl"] != float64(300) {
			t.Fatalf("the alias after its read = %+v", got)
		}
		if n := rig.count(`select count(*) from integration_events where request_id = $1 and topic in ('day2.pre', 'day2.post')
			and payload->'data'->'request'->>'resource_id' = $2`, run.Id, alias.Id); n != 2 {
			t.Fatalf("the action's day-2 events about the alias = %d", n)
		}

		// A composite checks each host a REST call lists, one at a time.
		// The second check fails: the third doesn't begin, the catch path
		// opens a ticket with what failed, and the request goes on.
		change := newChangeAPI(t)
		h.svc.REST.Hosts = []string{change.URL}
		hosts := map[string]any{"key": "hosts", "title": "List the hosts",
			"rest": map[string]any{"method": "GET", "url": change.URL + "/api/change/hosts", "keep": "$.result.hosts"}}
		check := map[string]any{"key": "check", "title": "Check a host",
			"rest": map[string]any{"method": "GET", "url": change.URL + "/api/change/${each}", "accept": []any{200}}}
		composite := rig.publishDefinition("check-hosts", askDefinition(false,
			hosts,
			caught(loopOf(check, "steps.hosts.json", 1), posting(change, "ticket", "{'failed': error.step, 'why': error.message}")),
			posting(change, "report", "{'caught': steps.check.caught, 'hosts': steps.hosts.json}"),
		))
		change.script(changeAnswer{200, map[string]any{"result": map[string]any{"hosts": []any{"web-01", "web-02", "web-03"}}}},
			changeAnswer{200, map[string]any{}}, changeAnswer{404, map[string]any{}})
		req := rig.submit(owner, composite)
		h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
		settle(t, ctx, tc, req.Id)
		if n := change.checked(); n != 3 {
			t.Fatalf("the change API was asked %d times, want the list and two checks", n)
		}
		st := rig.steps(req.Id, owner)
		if st["step:check/each:0"].Status != api.StepStatusDone || st["step:check/each:1"].Status != api.StepStatusFailed ||
			st["step:check/each:2"].Status != api.StepStatusSkipped || st["step:check"].Status != api.StepStatusFailed ||
			st["step:ticket"].Status != api.StepStatusDone || st["step:report"].Status != api.StepStatusDone {
			t.Fatalf("the steps = %+v %+v %+v %+v %+v %+v", st["step:check/each:0"], st["step:check/each:1"], st["step:check/each:2"],
				st["step:check"], st["step:ticket"], st["step:report"])
		}
		posted := change.got()
		if len(posted) != 2 || posted[0]["failed"] != "check" || !strings.Contains(fmt.Sprint(posted[0]["why"]), "404") ||
			posted[1]["caught"] != true || fmt.Sprint(posted[1]["hosts"]) != "[web-01 web-02 web-03]" {
			t.Fatalf("what the composite posted = %v", posted)
		}
		if out := rig.outputs(req.Id, "step:check"); out["caught"] != true {
			t.Fatalf("the loop's outputs = %v", out)
		}
	})
}
