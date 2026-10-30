package vra

import (
	"encoding/json"
	"time"
)

// SampleEstate is a vRA like the organisation's, for tests, the fake and
// the demo: three projects (one rvav doesn't have), cloud templates of one
// VM, of Windows with domain join, of a three-tier application behind an
// NSX load balancer and of a database server with a dropdown from a vRO
// action; custom forms, catalog items (a vRO workflow and an ABX action
// among them), sharing, approval, lease, day-2 and quota policies, and
// deployments of VMs named vra-* (one whose VM is gone, one whose owner
// left).
func SampleEstate(now time.Time) Estate {
	now = now.UTC().Truncate(time.Second)
	days := func(n int) *time.Time { t := now.AddDate(0, 0, n); return &t }
	user := func(e string) Principal { return Principal{Email: e, Type: "user"} }
	group := func(e string) Principal { return Principal{Email: e, Type: "group"} }
	rhel1 := rhelTemplate(false)
	rhel2 := rhelTemplate(true)
	e := Estate{
		ReadAt: now,
		Projects: []Project{
			{ID: "vra-prj-web", Name: "Web Platform", Description: "Front ends and APIs",
				Administrators: []Principal{user("lea.martin@corp.example")}, Members: []Principal{user("thomas.nguyen@corp.example"),
					group("rvav-web@corp.example")}, Viewers: []Principal{group("auditors@corp.example")},
				MachineNamingTemplate: "web-${input.environment}-${input.role}${###}", Properties: map[string]string{"costCenter": "CC-1001"}},
			{ID: "vra-prj-data", Name: "Data Services", Administrators: []Principal{user("marc.keller@corp.example")},
				Members: []Principal{user("lea.martin@corp.example")}},
			{ID: "vra-prj-legacy", Name: "Legacy Apps", Administrators: []Principal{user("former.admin@corp.example")}},
		},
		Blueprints: []Blueprint{
			{ID: "bp-rhel9", Name: "RHEL 9 Server", Description: "A RHEL 9 VM with its role and size.", ProjectID: "vra-prj-web",
				ProjectName: "Web Platform", Status: "RELEASED", Content: rhel2, RequestScopeOrg: true, Versions: []BlueprintVersion{
					{ID: "bp-rhel9-v1", BlueprintID: "bp-rhel9", Version: "1", Status: "VERSIONED", VersionDescription: "First", Content: rhel1},
					{ID: "bp-rhel9-v2", BlueprintID: "bp-rhel9", Version: "2", Status: "RELEASED", VersionDescription: "Cloud-init", Content: rhel2},
				}},
			{ID: "bp-win", Name: "Windows Server 2022", ProjectID: "vra-prj-web", Status: "RELEASED", Content: windowsTemplate,
				Versions: []BlueprintVersion{{ID: "bp-win-v3", BlueprintID: "bp-win", Version: "3", Status: "RELEASED", Content: windowsTemplate}}},
			{ID: "bp-app", Name: "Three-tier application", ProjectID: "vra-prj-web", Status: "RELEASED", Content: appTemplate,
				Versions: []BlueprintVersion{{ID: "bp-app-v1", BlueprintID: "bp-app", Version: "1.0", Status: "RELEASED", Content: appTemplate}}},
			{ID: "bp-db", Name: "Database server", ProjectID: "vra-prj-data", Status: "RELEASED", Content: dbTemplate,
				Versions: []BlueprintVersion{{ID: "bp-db-v1", BlueprintID: "bp-db", Version: "1", Status: "RELEASED", Content: dbTemplate}}},
			{ID: "bp-draft", Name: "Work in progress", ProjectID: "vra-prj-web", Status: "DRAFT", Content: rhel1},
		},
		Forms: []Form{
			{ID: "form-rhel9", Name: "RHEL 9 Server", SourceID: "bp-rhel9", SourceType: "com.vmw.blueprint", Type: "requestForm",
				Status: "ON", Form: rhelForm},
			{ID: "form-db", Name: "Database server", SourceID: "bp-db", SourceType: "com.vmw.blueprint", Type: "requestForm",
				Status: "ON", Form: dbForm},
		},
		CatalogItems: []CatalogItem{
			{ID: "ci-rhel9", Name: "RHEL 9 Server", Description: "A RHEL 9 VM.", Type: Ref{ID: "com.vmw.blueprint", Name: "VMware Cloud Templates"},
				SourceID: "bp-rhel9", SourceName: "RHEL 9 Server", ProjectIDs: []string{"vra-prj-web", "vra-prj-data", "vra-prj-legacy"},
				BulkRequestLimit: 5, FormID: "form-rhel9"},
			{ID: "ci-win", Name: "Windows Server 2022", Type: Ref{ID: "com.vmw.blueprint"}, SourceID: "bp-win",
				ProjectIDs: []string{"vra-prj-web"}, BulkRequestLimit: 1},
			{ID: "ci-app", Name: "Three-tier application", Type: Ref{ID: "com.vmw.blueprint"}, SourceID: "bp-app",
				ProjectIDs: []string{"vra-prj-web"}, BulkRequestLimit: 1},
			{ID: "ci-db", Name: "Database server", Type: Ref{ID: "com.vmw.blueprint"}, SourceID: "bp-db",
				ProjectIDs: []string{"vra-prj-data"}, BulkRequestLimit: 1, FormID: "form-db"},
			{ID: "ci-adgroup", Name: "Create AD group", Type: Ref{ID: "com.vmw.vro.workflow", Name: "vRealize Orchestrator Workflow"},
				SourceID: "wf-ad-group", ProjectIDs: []string{"vra-prj-web", "vra-prj-data"}},
			{ID: "ci-abx", Name: "Rotate backup key", Type: Ref{ID: "com.vmw.abx.actions", Name: "Extensibility actions"},
				SourceID: "abx-rotate", ProjectIDs: []string{"vra-prj-data"}},
		},
		Policies: []Policy{
			policy("pol-approval-prd", "Production needs a lead", PolicyApproval, "HARD", "",
				`{"matchExpression":[{"key":"input.environment","operator":"eq","value":"prd"}]}`,
				`{"level":1,"approvalMode":"ANY_OF","approvalType":"USER","approvers":["group:infra-leads@corp.example"],"autoApprovalDecision":"REJECT","autoApprovalExpiry":3,"actions":["Deployment.Create"]}`),
			policy("pol-lease-dev", "Development leases", PolicyLease, "HARD", "vra-prj-web", "",
				`{"leaseGrace":7,"leaseTermMax":30,"leaseTotalTermMax":90}`),
			policy("pol-day2", "Members run power and snapshots", PolicyDay2, "HARD", "", "",
				`{"allowedActions":[{"authorities":["ROLE:project_member"],"actions":["Cloud.vSphere.Machine.PowerOn","Cloud.vSphere.Machine.PowerOff","Cloud.vSphere.Machine.Snapshot.Create"]},{"authorities":["ROLE:project_administrator"],"actions":["Deployment.Delete","Deployment.ChangeOwner"]}]}`),
			policy("pol-quota-web", "Web quota", PolicyQuota, "HARD", "vra-prj-web", "",
				`{"level":"project","limits":{"cpu":64,"memory":256,"storage":2048,"instances":40}}`),
			policy("pol-limit", "Deployment limit", PolicyLimit, "HARD", "", "", `{"limits":{"cpu":16,"memory":64}}`),
		},
		Deployments: []Deployment{
			deployment("dep-vra-1", "vra-web-001", "vra-prj-web", "ci-rhel9", "bp-rhel9", "2", "thomas.nguyen@corp.example", days(20), now,
				map[string]any{"size": "small", "environment": "dev", "role": "web"}, machine("vra-web-001", "10.31.10.141", 2, 4096)),
			deployment("dep-vra-2", "vra-web-pair", "vra-prj-web", "ci-rhel9", "bp-rhel9", "1", "lea.martin@corp.example", days(45), now,
				map[string]any{"size": "medium", "environment": "dev", "role": "api"},
				machine("vra-api-001", "10.31.10.142", 4, 16384), machine("vra-api-002", "10.31.10.143", 4, 16384)),
			deployment("dep-vra-3", "vra-db-001", "vra-prj-data", "ci-db", "bp-db", "1", "lea.martin@corp.example", nil, now,
				map[string]any{"environment": "dev", "dbVersion": "16"}, machine("vra-db-001", "10.31.10.144", 4, 16384)),
			deployment("dep-vra-4", "vra-gone", "vra-prj-web", "ci-rhel9", "bp-rhel9", "2", "thomas.nguyen@corp.example", days(10), now,
				map[string]any{"size": "small", "environment": "dev", "role": "old"}, machine("vra-gone-001", "10.31.10.145", 2, 4096)),
			deployment("dep-vra-5", "vra-legacy", "vra-prj-legacy", "ci-rhel9", "bp-rhel9", "1", "former.admin@corp.example", nil, now,
				map[string]any{"size": "small", "environment": "dev", "role": "leg"}, machine("vra-leg-001", "10.31.10.146", 2, 4096)),
		},
	}
	for _, p := range e.Projects {
		for _, ci := range e.CatalogItems {
			for _, id := range ci.ProjectIDs {
				if id == p.ID {
					var en Entitlement
					en.ID, en.ProjectID = "ent-"+p.ID+"-"+ci.ID, p.ID
					en.Definition.Type, en.Definition.ID, en.Definition.Name = "CatalogItemIdentifier", ci.ID, ci.Name
					en.Definition.SourceType = ci.Type.ID
					e.Entitlements = append(e.Entitlements, en)
				}
			}
		}
	}
	return e
}

func policy(id, name, typ, enforcement, project, criteria, def string) Policy {
	p := Policy{ID: id, Name: name, TypeID: typ, EnforcementType: enforcement, ProjectID: project, Definition: json.RawMessage(def)}
	if criteria != "" {
		p.Criteria = json.RawMessage(criteria)
	}
	return p
}

func deployment(id, name, project, item, bp, version, owner string, lease *time.Time, now time.Time, inputs map[string]any,
	res ...Resource) Deployment {
	return Deployment{ID: id, Name: name, ProjectID: project, CatalogItemID: item, BlueprintID: bp, BlueprintVersion: version,
		CreatedAt: now.AddDate(0, -3, 0), CreatedBy: owner, OwnedBy: owner, LeaseExpireAt: lease, Status: "CREATE_SUCCESSFUL",
		Inputs: inputs, Resources: res}
}

func machine(name, ip string, cpus, memMB int) Resource {
	return Resource{ID: "res-" + name, Name: "Cloud_vSphere_Machine_1", Type: MachineType, Properties: map[string]any{
		"resourceName": name, "address": ip, "cpuCount": cpus, "totalMemoryMB": memMB, "powerState": "ON",
		"datastoreName": "vsan-gva-dev-cl01"}}
}

func rhelTemplate(cloudInit bool) string {
	t := `formatVersion: 1
inputs:
  size:
    type: string
    title: Size
    enum: [small, medium, large]
    default: small
  environment:
    type: string
    title: Environment
    enum: [dev, tst, prd]
  role:
    type: string
    title: Role
    pattern: ^[a-z]{2,12}$
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: ${input.size}
      networks:
        - network: ${resource.Cloud_vSphere_Network_1.id}
      constraints:
        - tag: env:${input.environment}
`
	if cloudInit {
		t += `      cloudConfig: |
        #cloud-config
        hostname: ${self.resourceName}
`
	}
	return t + `  Cloud_vSphere_Network_1:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
      constraints:
        - tag: net:app
`
}

const windowsTemplate = `formatVersion: 1
inputs:
  size:
    type: string
    enum: [medium, large]
  ou:
    type: string
    title: Organisational unit
    default: OU=Servers,DC=corp,DC=example
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    properties:
      image: win2022
      flavor: ${input.size}
      customizationSpec: win-domain-join
      networks:
        - network: ${resource.Cloud_vSphere_Network_1.id}
  Cloud_vSphere_Network_1:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
`

const appTemplate = `formatVersion: 1
inputs:
  webCount:
    type: integer
    title: Front ends
    minimum: 1
    maximum: 4
    default: 2
resources:
  web:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      count: ${input.webCount}
      networks:
        - network: ${resource.app_net.id}
  db:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: large
      attachedDisks:
        - source: ${resource.db_data.id}
      networks:
        - network: ${resource.app_net.id}
  db_data:
    type: Cloud.vSphere.Disk
    properties:
      capacityGb: 200
  app_net:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
  lb:
    type: Cloud.NSX.LoadBalancer
    properties:
      routes:
        - protocol: HTTPS
          port: '443'
      instances: ${resource.web[*].id}
`

const dbTemplate = `formatVersion: 1
inputs:
  environment:
    type: string
    enum: [dev, prd]
  dbVersion:
    type: string
    title: PostgreSQL version
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: medium
      networks:
        - network: ${resource.Cloud_vSphere_Network_1.id}
  Cloud_vSphere_Network_1:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
`

// The forms are vRA's: a layout of pages, sections and fields, and a
// schema per field.
const rhelForm = `{"layout":{"pages":[{"id":"page_general","title":"General","sections":[
{"id":"section_env","fields":[{"id":"environment","display":"dropDown","state":{"visible":true,"read-only":false}}]},
{"id":"section_size","fields":[{"id":"size","display":"radio","state":{"visible":true,"read-only":false}}]},
{"id":"section_role","fields":[{"id":"role","display":"textField","state":{"visible":true,"read-only":false}}]}]}]},
"schema":{
"environment":{"label":"Environment","type":{"dataType":"string","isMultiple":false},"valueList":[{"label":"Development","value":"dev"},{"label":"Test","value":"tst"},{"label":"Production","value":"prd"}],"constraints":{"required":true}},
"size":{"label":"Size","type":{"dataType":"string","isMultiple":false},"valueList":[{"label":"Small (2 vCPU, 4 GB)","value":"small"},{"label":"Medium (4 vCPU, 16 GB)","value":"medium"},{"label":"Large (8 vCPU, 32 GB)","value":"large"}],"default":"small","constraints":{"required":true}},
"role":{"label":"Role","type":{"dataType":"string","isMultiple":false},"placeholder":"app","signpost":"Lower-case letters, 2 to 12","constraints":{"required":true,"pattern":"^[a-z]{2,12}$"}}},
"options":{"externalValidations":[]}}`

const dbForm = `{"layout":{"pages":[{"id":"page_general","title":"Database","sections":[
{"id":"section_env","fields":[{"id":"environment","display":"dropDown","state":{"visible":true,"read-only":false}}]},
{"id":"section_version","fields":[{"id":"dbVersion","display":"dropDown","state":{"visible":true,"read-only":false}}]},
{"id":"section_backup","fields":[{"id":"backupWindow","display":"textField","state":{"visible":[{"equals":{"environment":"prd"},"value":true}],"read-only":false}}]}]}]},
"schema":{
"environment":{"label":"Environment","type":{"dataType":"string","isMultiple":false},"valueList":[{"label":"Development","value":"dev"},{"label":"Production","value":"prd"}],"constraints":{"required":true}},
"dbVersion":{"label":"PostgreSQL version","type":{"dataType":"string","isMultiple":false},"valueList":{"id":"com.corp.db/getVersions","type":"scriptAction","parameters":[{"$type":"field","value":"environment"}]},"constraints":{"required":true}},
"backupWindow":{"label":"Backup window","type":{"dataType":"string","isMultiple":false},"default":"02:00-04:00"}},
"options":{"externalValidations":[]}}`
