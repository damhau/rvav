package vra

import (
	"encoding/json"
	"strings"
	"time"
)

// StandardVRO is an estate of the vRO workflows an enterprise vRA typically
// has (D2.1): decommissioning with the owner's confirmation and a wait,
// change tickets waited for in ServiceNow, CMDB and backup registration,
// notifications, a patch window, a large disk approved by a manager, AD,
// guest scripts, a form's datastore choice, a rename, and a question to the
// requester mid-run. It sizes the composite step kinds rvav builds (D2)
// before the organisation's own vRA is read (#85): `rvav vro-triage
// -standard`.
func StandardVRO(now time.Time) Estate {
	ago := func(d int) *time.Time { t := now.Add(-time.Duration(d) * 24 * time.Hour); return &t }
	p := func(name, typ string) VROParam { return VROParam{Name: name, Type: typ} }
	item := func(m map[string]any) string { b, _ := json.Marshal(m); return string(b) }
	task := func(name, script string) string {
		return item(map[string]any{"name": name, "type": "task", "script": map[string]any{"value": script, "encoded": false}})
	}
	of := func(name, typ, display string) string {
		return item(map[string]any{"name": name, "type": typ, "display-name": display})
	}
	link := func(name, id string) string {
		return item(map[string]any{"name": name, "type": "link", "linked-workflow-id": id})
	}
	wf := func(id, name, category string, inputs []VROParam, runs, lastDays int, items ...string) VROWorkflow {
		content := `{"id":"` + id + `","display-name":"` + name + `","workflow-item":[` + strings.Join(items, ",") + `,{"name":"end","type":"end"}]}`
		return VROWorkflow{ID: id, Name: name, Category: category, Version: "1.0.0", Inputs: inputs, Content: content, Runs: runs,
			LastRun: ago(lastDays)}
	}
	rest := func(host, method, path string) string {
		return `var host = RESTHostManager.getHost("` + host + `");
var response = host.createRequest("` + method + `", "` + path + `", JSON.stringify(payload)).execute();
if (response.statusCode >= 300) throw "` + host + ` answered " + response.statusCode;`
	}
	mail := `var message = new EmailMessage();
message.toAddress = owner;
message.subject = subject;
message.addMimePart(body, "text/html; charset=UTF-8");
message.sendMessage();`

	e := Estate{ReadAt: now}
	e.Workflows = []VROWorkflow{
		// 1. Waits for the owner, then days, then decommissions and records the change.
		wf("std-decommission", "Decommission an application", "Corp/Lifecycle", []VROParam{p("deploymentId", "string"), p("owner", "string")},
			24, 10,
			of("confirm", "input", "The application owner confirms"),
			of("grace", "waiting-timer", "Wait seven days"),
			task("power-off", `for each (var vm in VcPlugin.getAllVirtualMachines()) { if (vm.name.indexOf(deploymentId) == 0) vm.powerOffVM_Task(); }`),
			task("change", rest("servicenow", "POST", "/api/now/table/change_request"))),
		// 2. Opens a change, then polls ServiceNow until it is approved or refused.
		wf("std-change-wait", "Change ticket, then wait for its approval in ServiceNow", "Corp/ITSM",
			[]VROParam{p("summary", "string"), p("risk", "string")}, 310, 1,
			task("open", rest("servicenow", "POST", "/api/now/table/change_request")),
			of("poll", "waiting-timer", "Every ten minutes"),
			task("state", rest("servicenow", "GET", "/api/now/table/change_request/{sys_id}")),
			of("approved", "decision", "Approved?")),
		// 3. Registers the VM in the CMDB, and removes it when deleted.
		wf("std-cmdb", "Register the VM in the CMDB", "Corp/CMDB", []VROParam{p("vm", "VC:VirtualMachine"), p("payload", "Properties")}, 2400, 0,
			task("register", rest("cmdb", "POST", "/api/ci"))),
		// 4. Registers the VM in the backup tool's policy.
		wf("std-backup", "Register the VM in backup", "Corp/Backup", []VROParam{p("vm", "VC:VirtualMachine"), p("policy", "string")}, 1900, 0,
			task("register", rest("veeam", "POST", "/api/v1/jobs/{policy}/includes"))),
		// 5. Tells the owner, by mail, that their VM is ready.
		wf("std-notify", "Tell the owner the VM is ready", "Corp/Notifications", []VROParam{p("owner", "string"), p("subject", "string"),
			p("body", "string")}, 2600, 0, task("mail", mail)),
		// 6. Snapshot, wait for the maintenance window, patch, delete the snapshot days later.
		wf("std-patch-window", "Patch in the maintenance window", "Corp/Patching", []VROParam{p("vm", "VC:VirtualMachine"), p("window", "Date")}, 140, 7,
			task("snapshot", `vm.createSnapshot_Task("before-patch", "", false, false);`),
			of("window", "waiting-timer", "Until the maintenance window"),
			link("patch", "std-guest-script"),
			of("keep", "waiting-timer", "Keep the snapshot three days"),
			task("cleanup", `vm.removeAllSnapshots_Task();`)),
		// 7. A disk over 500 GB waits for the manager.
		wf("std-big-disk", "Add a disk, a manager approving above 500 GB", "Corp/Day-2", []VROParam{p("vm", "VC:VirtualMachine"),
			p("sizeGb", "number"), p("manager", "string")}, 85, 3,
			of("large", "decision", "Above 500 GB?"),
			of("approve", "input", "The manager approves"),
			task("add", `var spec = new VcVirtualMachineConfigSpec(); /* the disk */ vm.reconfigVM_Task(spec);`)),
		// 8. The VM's computer object and groups in AD.
		wf("std-ad-computer", "Create the AD computer object and add it to groups", "Corp/Active Directory",
			[]VROParam{p("name", "string"), p("ou", "AD:OrganizationalUnit"), p("groups", "Array/string")}, 1800, 0,
			task("create", `var computer = ActiveDirectory.createComputerAD(name, ou);
for each (var g in groups) { ActiveDirectory.getUserGroup(g).addElements([computer]); }`)),
		// 9. A script in the guest.
		wf("std-guest-script", "Run a script in the guest", "Corp/Guest", []VROParam{p("vm", "VC:VirtualMachine"), p("script", "string")}, 650, 1,
			task("run", `var host = PowerShellHostManager.getHost("jump01"); var session = host.openSession(); session.invokeScript(script);`)),
		// 11. Renames the VM in vCenter.
		wf("std-rename", "Rename a VM", "Corp/vCenter", []VROParam{p("vm", "VC:VirtualMachine"), p("name", "string")}, 60, 12,
			task("rename", `vm.rename_Task(name);`)),
		// 12. Asks the requester for what's missing, then goes on.
		wf("std-ask-requester", "Ask the requester for a missing detail", "Corp/Lifecycle", []VROParam{p("requester", "string"),
			p("question", "string")}, 40, 15,
			of("ask", "input", "The requester answers"),
			task("record", rest("cmdb", "PATCH", "/api/ci/{id}"))),
	}
	// 10. A form's datastore choice: the one with the most free space.
	e.Actions = []VROAction{{ID: "std-act-datastore", Name: "roomiestDatastore", Module: "com.corp.vc", Version: "1.0.0",
		Inputs: []VROParam{p("cluster", "string")}, OutputType: "Array/string",
		Script: `var stores = VcPlugin.getAllDatastores(); stores.sort(function (a, b) { return b.summary.freeSpace - a.summary.freeSpace; });
return stores.map(function (d) { return d.name; });`}}
	e.Forms = []Form{{ID: "std-form", Name: "Linux server", SourceID: "bp-std", SourceType: "com.vmw.blueprint", Type: "requestForm",
		Status: "ON", Form: `{"schema":{"datastore":{"label":"Datastore","valueList":{"id":"com.corp.vc/roomiestDatastore","type":"scriptAction"}}}}`}}
	return e
}
