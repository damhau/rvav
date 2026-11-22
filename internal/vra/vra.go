// Package vra reads vRealize Automation 8 / Aria Automation through its REST
// API, for the importer (P2, ADR-0027): projects, cloud templates and their
// versions, custom forms, catalog items and their sharing, policies and
// deployments. It only reads; nothing in vRA is changed.
//
// The calls follow vRA 8's API reference (iaas, blueprint, form-service,
// catalog, policy and deployment services). FakeServer answers them the
// same way, for tests and the development stack; a real vRA confirms them
// (Lab acceptance).
package vra

import (
	"encoding/json"
	"slices"
	"time"
)

// Principal is a project's administrator, member or viewer.
type Principal struct {
	Email string `json:"email"`
	Type  string `json:"type"` // user or group
}

// Project is a vRA project.
type Project struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name"`
	Description           string            `json:"description,omitempty"`
	Administrators        []Principal       `json:"administrators,omitempty"`
	Members               []Principal       `json:"members,omitempty"`
	Viewers               []Principal       `json:"viewers,omitempty"`
	Properties            map[string]string `json:"customProperties,omitempty"`
	MachineNamingTemplate string            `json:"machineNamingTemplate,omitempty"`
	// PlacementPolicy is how vRA chooses where the project's machines go:
	// DEFAULT, SPREAD or SPREAD_MEMORY (BINPACK on a cloud zone).
	PlacementPolicy string `json:"placementPolicy,omitempty"`
	// SharedResources is the project's Deployment sharing: its members see
	// each other's deployments. vRA's default, when absent, is on.
	SharedResources *bool `json:"sharedResources,omitempty"`
}

// CustomNaming is one of vRA's custom names (Infrastructure › Custom
// names): the patterns its projects' resources are named by, each with a
// counter that starts and steps as set.
type CustomNaming struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Projects  []NamingProject  `json:"projects,omitempty"`
	Templates []NamingTemplate `json:"templates,omitempty"`
}

// NamingProject is a project a custom naming is for, or the organisation's
// default.
type NamingProject struct {
	ProjectID  string `json:"projectId,omitempty"`
	OrgDefault bool   `json:"orgDefault,omitempty"`
	Active     bool   `json:"active,omitempty"`
}

// NamingTemplate is a custom naming's pattern for one resource type
// (COMPUTE for machines).
type NamingTemplate struct {
	ResourceType  string `json:"resourceType"`
	Pattern       string `json:"pattern"`
	StartCounter  int    `json:"startCounter"`
	IncrementStep int    `json:"incrementStep"`
}

// PropertyGroup is one of vRA's property groups (Design › Property
// groups): inputs several templates share, or constants they read.
type PropertyGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	// Type is INPUT or CONSTANT.
	Type      string `json:"type"`
	ProjectID string `json:"projectId,omitempty"`
	// Properties are JSON schemas: an input's, or a constant's with its
	// value in const.
	Properties map[string]map[string]any `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

// PropertyGroupFor is the property group a project's template names:
// the project's own, else the organisation's; nil when there is none.
func (e *Estate) PropertyGroupFor(name, projectID string) *PropertyGroup {
	var org *PropertyGroup
	for i, g := range e.PropertyGroups {
		switch {
		case g.Name != name:
		case g.ProjectID == projectID:
			return &e.PropertyGroups[i]
		case g.ProjectID == "" && org == nil:
			org = &e.PropertyGroups[i]
		}
	}
	return org
}

// MachineNaming is the custom naming pattern of a project's machines,
// its own before the organisation's default; nil when there is none.
func (e *Estate) MachineNaming(projectID string) (*CustomNaming, *NamingTemplate) {
	var def *CustomNaming
	for i, n := range e.Namings {
		for _, p := range n.Projects {
			switch {
			case p.ProjectID == projectID && p.Active:
				if t := n.compute(); t != nil {
					return &e.Namings[i], t
				}
			case p.OrgDefault && def == nil:
				def = &e.Namings[i]
			}
		}
	}
	if def != nil {
		if t := def.compute(); t != nil {
			return def, t
		}
	}
	return nil, nil
}

func (n CustomNaming) compute() *NamingTemplate {
	i := slices.IndexFunc(n.Templates, func(t NamingTemplate) bool { return t.ResourceType == "COMPUTE" })
	if i < 0 {
		return nil
	}
	return &n.Templates[i]
}

// ImageProfile maps image names (what templates ask for) to a region's
// vCenter templates.
type ImageProfile struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	RegionID      string `json:"regionId,omitempty"`
	ImageMappings struct {
		Mapping map[string]ImageMapping `json:"mapping"`
	} `json:"imageMappings"`
}

// ImageMapping is the template an image name stands for in its profile's
// region, with the cloud-init and constraints vRA gives its machines.
type ImageMapping struct {
	ID          string            `json:"id,omitempty"`
	Name        string            `json:"name"` // the vCenter template
	CloudConfig string            `json:"cloudConfig,omitempty"`
	Constraints []ImageConstraint `json:"constraints,omitempty"`
}

// ImageConstraint is a constraint tag of an image mapping, key:value; a
// soft one when not mandatory.
type ImageConstraint struct {
	Mandatory  bool   `json:"mandatory"`
	Expression string `json:"expression"`
}

// Region is a cloud account's datacenter, where an image profile's
// templates are.
type Region struct {
	ID               string `json:"id"`
	Name             string `json:"name,omitempty"`
	ExternalRegionID string `json:"externalRegionId,omitempty"`
	CloudAccountID   string `json:"cloudAccountId,omitempty"`
}

// CloudAccount is a vSphere cloud account: HostName is its vCenter.
type CloudAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	HostName string `json:"hostName,omitempty"`
}

// FlavorProfile maps flavor names (sizes) to CPU and memory.
type FlavorProfile struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	RegionID       string `json:"regionId,omitempty"`
	FlavorMappings struct {
		Mapping map[string]FlavorMapping `json:"mapping"`
	} `json:"flavorMappings"`
}

// FlavorMapping is a flavor's size.
type FlavorMapping struct {
	CPUCount   int `json:"cpuCount"`
	MemoryInMB int `json:"memoryInMB"`
}

// Blueprint is a cloud template; Content is its YAML. A template shared
// with every project of the organisation has RequestScopeOrg.
type Blueprint struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Description     string             `json:"description,omitempty"`
	ProjectID       string             `json:"projectId"`
	ProjectName     string             `json:"projectName,omitempty"`
	Status          string             `json:"status,omitempty"` // DRAFT, VERSIONED, RELEASED
	Content         string             `json:"content,omitempty"`
	RequestScopeOrg bool               `json:"requestScopeOrg,omitempty"`
	Versions        []BlueprintVersion `json:"versions,omitempty"` // filled by the reader
}

// BlueprintVersion is a version of a cloud template.
type BlueprintVersion struct {
	ID                 string `json:"id"`
	BlueprintID        string `json:"blueprintId"`
	Version            string `json:"version"`
	Status             string `json:"status"` // VERSIONED, RELEASED
	VersionDescription string `json:"versionDescription,omitempty"`
	Content            string `json:"content,omitempty"`
}

// Form is a custom request form; Form holds its definition (layout and
// schema) as JSON text, as vRA stores it.
type Form struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	SourceID   string `json:"sourceId"`
	SourceType string `json:"sourceType"` // com.vmw.blueprint, com.vmw.vro.workflow...
	Type       string `json:"type"`       // requestForm
	Status     string `json:"status"`     // ON, OFF
	Form       string `json:"form"`
}

// Ref is a type or source vRA names.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// CatalogItem is an item of vRA's service catalog.
type CatalogItem struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Type             Ref      `json:"type"` // com.vmw.blueprint, com.vmw.vro.workflow, com.vmw.abx.actions...
	SourceID         string   `json:"sourceId,omitempty"`
	SourceName       string   `json:"sourceName,omitempty"`
	ProjectIDs       []string `json:"projectIds,omitempty"`
	BulkRequestLimit int      `json:"bulkRequestLimit,omitempty"`
	FormID           string   `json:"formId,omitempty"`
}

// Entitlement shares a content source or an item with a project.
type Entitlement struct {
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	Definition struct {
		Type       string `json:"type"` // CatalogSourceIdentifier, CatalogItemIdentifier
		ID         string `json:"id"`
		Name       string `json:"name,omitempty"`
		SourceType string `json:"sourceType,omitempty"`
	} `json:"definition"`
}

// Policy is an approval, lease, day-2 action, quota or limit policy; its
// definition depends on TypeID.
type Policy struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	TypeID          string          `json:"typeId"`
	EnforcementType string          `json:"enforcementType,omitempty"` // HARD, SOFT
	ProjectID       string          `json:"projectId,omitempty"`
	Criteria        json.RawMessage `json:"criteria,omitempty"`
	Definition      json.RawMessage `json:"definition,omitempty"`
}

// Policy types.
const (
	PolicyApproval = "com.vmware.policy.approval"
	PolicyLease    = "com.vmware.policy.deployment.lease"
	PolicyDay2     = "com.vmware.policy.deployment.action"
	PolicyQuota    = "com.vmware.policy.resource.quota"
	PolicyLimit    = "com.vmware.policy.deployment.limit"
	// PolicySharing is a content sharing policy: catalog items or content
	// sources shared with a project's users, groups or roles, or with all
	// of its members (vRA 8.x).
	PolicySharing = "com.vmware.policy.catalog.entitlement"
)

// SharingDefinition is a content sharing policy's definition.
type SharingDefinition struct {
	EntitledUsers []EntitledUsers `json:"entitledUsers"`
}

// EntitledUsers are principals and the items shared with them.
type EntitledUsers struct {
	UserType   string            `json:"userType,omitempty"` // USER, ROLE
	Principals []SharePrincipal  `json:"principals"`
	Items      []SharedReference `json:"items"`
}

// SharePrincipal is who a content sharing policy shares with: PROJECT (all
// its members), USER, GROUP or ROLE, by reference.
type SharePrincipal struct {
	Type        string `json:"type"`
	ReferenceID string `json:"referenceId"`
}

// SharedReference is a catalog item (CATALOG_ITEM_IDENTIFIER) or a content
// source (CATALOG_SOURCE_IDENTIFIER) a policy shares.
type SharedReference struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Share is how a catalog item is shared with a vRA project: with all its
// members, or with the principals content sharing policies name.
type Share struct {
	ProjectID  string
	All        bool
	Principals []SharePrincipal
}

// Resource is something a deployment holds: a machine, a disk, a network.
// Name is its resource's name in the cloud template, with [n] for each VM
// of a machine with a count.
type Resource struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"` // Cloud.vSphere.Machine, Cloud.vSphere.Disk...
	Properties map[string]any `json:"properties,omitempty"`
	// PreventDelete is the resource's preventDelete flag, should the
	// deployment API give one. vRA's reference documents none: the flag is
	// the cloud template's, outside the resource's properties
	// ("Automation Assembler resource flags for requests",
	// https://techdocs.broadcom.com/us/en/vmware-cis/aria/aria-automation/all/resource-flags-for-requests.html),
	// and a deployment's resources (id, name, type, properties, dependsOn,
	// state, syncStatus...) and the IaaS API's machines have no field for
	// it. It is read where the resource has its other flag, dependsOn, and
	// the importer also reads the template's (vraimport.Protected).
	// Estates stored without it read as nil.
	PreventDelete *bool `json:"preventDelete,omitempty"`
}

// Deployment is what a vRA request made.
type Deployment struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Description        string         `json:"description,omitempty"`
	ProjectID          string         `json:"projectId"`
	CatalogItemID      string         `json:"catalogItemId,omitempty"`
	CatalogItemVersion string         `json:"catalogItemVersion,omitempty"`
	BlueprintID        string         `json:"blueprintId,omitempty"`
	BlueprintVersion   string         `json:"blueprintVersion,omitempty"`
	CreatedAt          time.Time      `json:"createdAt"`
	CreatedBy          string         `json:"createdBy,omitempty"`
	OwnedBy            string         `json:"ownedBy,omitempty"`
	LeaseExpireAt      *time.Time     `json:"leaseExpireAt,omitempty"`
	Status             string         `json:"status,omitempty"`
	Inputs             map[string]any `json:"inputs,omitempty"`
	Resources          []Resource     `json:"resources,omitempty"`
}

// Subscription runs a vRO workflow or an ABX action on a vRA event.
type Subscription struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	EventTopicID string `json:"eventTopicId"`
	RunnableType string `json:"runnableType"` // extensibility.vro, extensibility.abx
	RunnableID   string `json:"runnableId"`
	Blocking     bool   `json:"blocking,omitempty"`
	Disabled     bool   `json:"disabled,omitempty"`
	// What runs when a blocking one fails, if anything.
	RecoverRunnableType string `json:"recoverRunnableType,omitempty"`
	RecoverRunnableID   string `json:"recoverRunnableId,omitempty"`
	// Constraints narrow it: to some projects (none: every project).
	Constraints struct {
		ProjectID []string `json:"projectId,omitempty"`
	} `json:"constraints"`
}

// ResourceAction is a custom day-2 action: a vRO workflow or an ABX action
// run on a resource or a deployment.
type ResourceAction struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName,omitempty"`
	ResourceType string `json:"resourceType"` // Deployment, Cloud.vSphere.Machine...
	Status       string `json:"status,omitempty"`
	RunnableItem struct {
		ID   string `json:"id"`
		Name string `json:"name,omitempty"`
		Type string `json:"type"` // vro.workflow, abx.action
	} `json:"runnableItem"`
}

// VROParam is a vRO workflow's or action's input.
type VROParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // string, VC:VirtualMachine, AD:User, REST:RESTHost...
	Description string `json:"description,omitempty"`
}

// VROWorkflow is a vRO workflow: its inputs, its content (the schema, as
// vRO gives it) and when it last ran.
type VROWorkflow struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Category    string     `json:"category,omitempty"`
	Description string     `json:"description,omitempty"`
	Version     string     `json:"version,omitempty"`
	Inputs      []VROParam `json:"inputs,omitempty"`
	Content     string     `json:"content,omitempty"`
	LastRun     *time.Time `json:"last_run,omitempty"`
	Runs        int        `json:"runs"`
}

// VROAction is a vRO action, with its script.
type VROAction struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Module     string     `json:"module"`
	Version    string     `json:"version,omitempty"`
	Inputs     []VROParam `json:"inputs,omitempty"`
	OutputType string     `json:"output_type,omitempty"`
	Script     string     `json:"script,omitempty"`
}

// FQN is how forms and scripts name it: module/name.
func (a VROAction) FQN() string { return a.Module + "/" + a.Name }

// Machine types whose VMs rvav can adopt.
const MachineType = "Cloud.vSphere.Machine"

// Estate is everything the importer reads from vRA, also what `rvav vra
// export` writes, to import from a file.
type Estate struct {
	ReadAt   time.Time       `json:"read_at"`
	URL      string          `json:"url"`
	Projects []Project       `json:"projects"`
	Images   []ImageProfile  `json:"image_profiles"`
	Flavors  []FlavorProfile `json:"flavor_profiles"`
	// Regions and the vSphere cloud accounts they are in say which
	// vCenter an image profile's templates are in.
	Regions       []Region       `json:"regions,omitempty"`
	CloudAccounts []CloudAccount `json:"cloud_accounts,omitempty"`
	// Namings are the custom names; none in a vRA without them.
	Namings []CustomNaming `json:"custom_namings,omitempty"`
	// PropertyGroups are the property groups; none in a vRA without them.
	PropertyGroups []PropertyGroup `json:"property_groups,omitempty"`
	Blueprints     []Blueprint     `json:"blueprints"`
	Forms          []Form          `json:"forms"`
	CatalogItems   []CatalogItem   `json:"catalog_items"`
	Entitlements   []Entitlement   `json:"entitlements"`
	Policies       []Policy        `json:"policies"`
	Deployments    []Deployment    `json:"deployments"`
	// What extensibility runs: vRA's subscriptions, vRO's workflows and
	// actions (the triage report, P2.4).
	Subscriptions   []Subscription   `json:"subscriptions"`
	ResourceActions []ResourceAction `json:"resource_actions"`
	Workflows       []VROWorkflow    `json:"vro_workflows"`
	Actions         []VROAction      `json:"vro_actions"`
}

// VCenterOf is the vCenter of a region: its vSphere cloud account's host
// name, "" when unknown.
func (e *Estate) VCenterOf(regionID string) string {
	i := slices.IndexFunc(e.Regions, func(r Region) bool { return r.ID == regionID })
	if i < 0 {
		return ""
	}
	j := slices.IndexFunc(e.CloudAccounts, func(a CloudAccount) bool { return a.ID == e.Regions[i].CloudAccountID })
	if j < 0 {
		return ""
	}
	return e.CloudAccounts[j].HostName
}

// RegionName is a region's name, else its ID.
func (e *Estate) RegionName(regionID string) string {
	if i := slices.IndexFunc(e.Regions, func(r Region) bool { return r.ID == regionID }); i >= 0 && e.Regions[i].Name != "" {
		return e.Regions[i].Name
	}
	return regionID
}

// SourceOf is what a catalog item offers: the cloud template or vRO
// workflow of its own ID, as vRA 8 names them, or else of its sourceId
// (which vRA gives the item's content source); an ABX action's ID.
func (e *Estate) SourceOf(ci CatalogItem) string {
	has := func(id string) bool {
		switch ci.Type.ID {
		case "com.vmw.blueprint":
			return slices.ContainsFunc(e.Blueprints, func(b Blueprint) bool { return b.ID == id })
		case "com.vmw.vro.workflow":
			return slices.ContainsFunc(e.Workflows, func(w VROWorkflow) bool { return w.ID == id })
		}
		return false
	}
	if !has(ci.ID) && ci.SourceID != "" && has(ci.SourceID) {
		return ci.SourceID
	}
	return ci.ID
}

// SharedWith are the projects a catalog item is offered to: shared on its
// own, through its content source, as the item lists them, or by a content
// sharing policy.
func (e *Estate) SharedWith(ci CatalogItem) []string {
	var out []string
	for _, s := range e.Shares(ci) {
		out = append(out, s.ProjectID)
	}
	return out
}

// Shares say how a catalog item is shared with each project, in the
// estate's order. An entitlement, or a policy's PROJECT principal, shares
// it with all the project's members; else the principals of the policies
// that name it do. A project the item only lists is all its members' (a
// vRA whose policies weren't read).
func (e *Estate) Shares(ci CatalogItem) []Share {
	all := map[string]bool{}
	listed := map[string]bool{}
	for _, id := range ci.ProjectIDs {
		listed[id] = true
	}
	for _, en := range e.Entitlements {
		switch en.Definition.Type {
		case "CatalogItemIdentifier":
			all[en.ProjectID] = all[en.ProjectID] || en.Definition.ID == ci.ID
		case "CatalogSourceIdentifier":
			all[en.ProjectID] = all[en.ProjectID] || ci.SourceID != "" && en.Definition.ID == ci.SourceID
		}
	}
	named := map[string][]SharePrincipal{}
	for _, p := range e.Policies {
		if p.TypeID != PolicySharing {
			continue
		}
		var def SharingDefinition
		if json.Unmarshal(p.Definition, &def) != nil {
			continue
		}
		for _, eu := range def.EntitledUsers {
			if !slices.ContainsFunc(eu.Items, func(r SharedReference) bool {
				return r.Type == "CATALOG_ITEM_IDENTIFIER" && r.ID == ci.ID ||
					r.Type == "CATALOG_SOURCE_IDENTIFIER" && ci.SourceID != "" && r.ID == ci.SourceID
			}) {
				continue
			}
			for _, vp := range e.Projects {
				if p.ProjectID != "" && p.ProjectID != vp.ID {
					continue
				}
				for _, pr := range eu.Principals {
					if pr.Type == "PROJECT" {
						all[vp.ID] = true
					} else if !slices.Contains(named[vp.ID], pr) {
						named[vp.ID] = append(named[vp.ID], pr)
					}
				}
			}
		}
	}
	var out []Share
	for _, p := range e.Projects {
		switch {
		case all[p.ID]:
			out = append(out, Share{ProjectID: p.ID, All: true})
		case len(named[p.ID]) > 0:
			out = append(out, Share{ProjectID: p.ID, Principals: named[p.ID]})
		case listed[p.ID]:
			out = append(out, Share{ProjectID: p.ID, All: true})
		}
	}
	return out
}

// ItemsFor are the catalog items offered to a project.
func (e *Estate) ItemsFor(projectID string) []CatalogItem {
	var out []CatalogItem
	for _, ci := range e.CatalogItems {
		if slices.Contains(e.SharedWith(ci), projectID) {
			out = append(out, ci)
		}
	}
	return out
}
