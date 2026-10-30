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
	Properties            map[string]string `json:"properties,omitempty"`
	MachineNamingTemplate string            `json:"machineNamingTemplate,omitempty"`
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

// ImageMapping is the template an image name stands for.
type ImageMapping struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"` // the vCenter template
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
)

// Resource is something a deployment holds: a machine, a disk, a network.
type Resource struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"` // Cloud.vSphere.Machine, Cloud.vSphere.Disk...
	Properties map[string]any `json:"properties,omitempty"`
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

// Machine types whose VMs rvav can adopt.
const MachineType = "Cloud.vSphere.Machine"

// Estate is everything the importer reads from vRA, also what `rvav vra
// export` writes, to import from a file.
type Estate struct {
	ReadAt       time.Time       `json:"read_at"`
	URL          string          `json:"url"`
	Projects     []Project       `json:"projects"`
	Images       []ImageProfile  `json:"image_profiles"`
	Flavors      []FlavorProfile `json:"flavor_profiles"`
	Blueprints   []Blueprint     `json:"blueprints"`
	Forms        []Form          `json:"forms"`
	CatalogItems []CatalogItem   `json:"catalog_items"`
	Entitlements []Entitlement   `json:"entitlements"`
	Policies     []Policy        `json:"policies"`
	Deployments  []Deployment    `json:"deployments"`
}
