package store

import (
	"encoding/json"
	"time"
)

type User struct {
	ID          string   `db:"id"`
	Subject     string   `db:"subject"`
	Name        string   `db:"name"`
	Email       string   `db:"email"`
	Title       string   `db:"title"`
	GlobalRoles []string `db:"global_roles"`
}

type Environment struct {
	Key        string `db:"key"`
	Name       string `db:"name"`
	Production bool   `db:"production"`
	SortOrder  int    `db:"sort_order"`
	// Snapshot retention: at most SnapshotMaxCount per VM, deleted after
	// SnapshotMaxAgeDays (nil: kept).
	SnapshotMaxCount   int  `db:"snapshot_max_count"`
	SnapshotMaxAgeDays *int `db:"snapshot_max_age_days"`
}

type Project struct {
	ID           string   `db:"id"`
	Code         string   `db:"code"`
	Name         string   `db:"name"`
	Description  string   `db:"description"`
	CostCenter   string   `db:"cost_center"`
	Environments []string `db:"environments"`
}

type Membership struct {
	ProjectID string `db:"project_id"`
	Role      string `db:"role"`
}

type QuotaLimit struct {
	Environment string `db:"environment"`
	Resource    string `db:"resource"`
	Limit       *int   `db:"limit_value"`
	Enforcement string `db:"enforcement"`
}

// QuotaAmounts holds amounts per quota resource (vcpu, memory_gb, storage_gb, vm_count).
type QuotaAmounts map[string]int

type PlacementRule struct {
	ID                string   `db:"id"`
	ProjectID         string   `db:"project_id"`
	Environment       string   `db:"environment"`
	Priority          int      `db:"priority"`
	ClusterPatterns   []string `db:"cluster_patterns"`
	DatastorePatterns []string `db:"datastore_patterns"`
	NetworkPatterns   []string `db:"network_patterns"`
	Folder            string   `db:"folder"`
}

type NetworkSubnet struct {
	Network             string   `db:"network"`
	Subnet              string   `db:"subnet"`
	Gateway             string   `db:"gateway"`
	DNSZone             string   `db:"dns_zone"`
	DNSServers          []string `db:"dns_servers"`
	EfficientIPSubnetID string   `db:"efficientip_subnet_id"`
}

type LeasePolicy struct {
	Environment   string `db:"environment"`
	DefaultDays   *int   `db:"default_days"`
	MaxDays       *int   `db:"max_days"`
	MaxExtensions int    `db:"max_extensions"`
	ExpiryAction  string `db:"expiry_action"`
}

type Category struct {
	Key       string `db:"key"`
	Name      string `db:"name"`
	ItemCount int    `db:"item_count"`
}

type CatalogItem struct {
	ID                 string     `db:"id"`
	Slug               string     `db:"slug"`
	Name               string     `db:"name"`
	Summary            string     `db:"summary"`
	DescriptionMD      string     `db:"description_md"`
	Category           string     `db:"category"`
	Type               string     `db:"type"`
	Icon               string     `db:"icon"`
	Tags               []string   `db:"tags"`
	Featured           bool       `db:"featured"`
	SourceKind         string     `db:"source_kind"`
	AWXTemplateID      *int       `db:"awx_template_id"`
	AWXTemplateName    *string    `db:"awx_template_name"`
	PublishedVersionID *string    `db:"published_version_id"`
	VersionNumber      *string    `db:"version_number"`
	VersionPublishedAt *time.Time `db:"version_published_at"`
	Favourite          bool       `db:"favourite"`
	Requests30d        int        `db:"requests_30d"`
	TypicalSeconds     *int       `db:"typical_seconds"`
	HasApprovalPolicy  bool       `db:"has_approval_policy"`
	AlwaysApproval     bool       `db:"always_approval"`
}

type ItemVersion struct {
	ID            string          `db:"id"`
	ItemID        string          `db:"item_id"`
	Number        string          `db:"number"`
	State         string          `db:"state"`
	FormSchema    json.RawMessage `db:"form_schema"`
	UISchema      json.RawMessage `db:"ui_schema"`
	InitialValues json.RawMessage `db:"initial_values"`
	Blueprint     json.RawMessage `db:"blueprint"`
	PublishedAt   *time.Time      `db:"published_at"`
}

type ApprovalPolicy struct {
	ID        string          `db:"id"`
	Name      string          `db:"name"`
	Reason    string          `db:"reason"`
	Condition string          `db:"condition"`
	ItemType  *string         `db:"item_type"`
	ProjectID *string         `db:"project_id"`
	Levels    json.RawMessage `db:"levels"`
	Priority  int             `db:"priority"`
	// AutoApprove is CEL: when true the policy is satisfied without anyone.
	AutoApprove *string `db:"auto_approve"`
}

type Request struct {
	ID               string          `db:"id"`
	Number           string          `db:"number"`
	ItemID           string          `db:"item_id"`
	VersionID        string          `db:"version_id"`
	ProjectID        string          `db:"project_id"`
	Environment      string          `db:"environment"`
	Title            string          `db:"title"`
	Status           string          `db:"status"`
	RequesterID      string          `db:"requester_id"`
	OnBehalfOfID     *string         `db:"on_behalf_of_id"`
	OwnerID          string          `db:"owner_id"`
	Inputs           json.RawMessage `db:"inputs"`
	Count            int             `db:"count"`
	Justification    string          `db:"justification"`
	LeaseDays        *int            `db:"lease_days"`
	ApprovalRequired bool            `db:"approval_required"`
	Approval         json.RawMessage `db:"approval"`
	Plan             json.RawMessage `db:"plan"`
	SubmittedAt      time.Time       `db:"submitted_at"`
	ScheduledAt      *time.Time      `db:"scheduled_at"`
	StartedAt        *time.Time      `db:"started_at"`
	FinishedAt       *time.Time      `db:"finished_at"`
	WorkflowID       *string         `db:"workflow_id"`
	ErrorStepID      *string         `db:"error_step_id"`
	ErrorMessage     *string         `db:"error_message"`
	// Kind is provision or day2; a day-2 request runs Action on a deployment.
	Kind         string  `db:"kind"`
	Action       *string `db:"action"`
	DeploymentID *string `db:"deployment_id"`
	ResourceID   *string `db:"resource_id"`
	ResourceName *string `db:"resource_name"`
}

// RequestRow is a request joined with the names the API shows next to it.
type RequestRow struct {
	Request
	ItemSlug         string   `db:"item_slug"`
	ItemName         string   `db:"item_name"`
	ItemType         string   `db:"item_type"`
	VersionNumber    string   `db:"version_number"`
	ProjectCode      string   `db:"project_code"`
	ProjectName      string   `db:"project_name"`
	RequesterName    string   `db:"requester_name"`
	RequesterEmail   string   `db:"requester_email"`
	OnBehalfOfName   *string  `db:"on_behalf_of_name"`
	OnBehalfOfEmail  *string  `db:"on_behalf_of_email"`
	DeploymentIDs    []string `db:"deployment_ids"`
	StepsDone        int      `db:"steps_done"`
	StepsTotal       int      `db:"steps_total"`
	CurrentStepID    *string  `db:"current_step_id"`
	CurrentStepTitle *string  `db:"current_step_title"`
}

type Step struct {
	ID           string     `db:"id"`
	RequestID    string     `db:"request_id"`
	ParentID     *string    `db:"parent_id"`
	Position     int        `db:"position"`
	Key          string     `db:"key"`
	Title        string     `db:"title"`
	Detail       string     `db:"detail"`
	System       *string    `db:"system"`
	Status       string     `db:"status"`
	StartedAt    *time.Time `db:"started_at"`
	FinishedAt   *time.Time `db:"finished_at"`
	Error        *string    `db:"error"`
	ExternalKind *string    `db:"external_kind"`
	ExternalID   *string    `db:"external_id"`
	ExternalURL  *string    `db:"external_url"`
	HasLog       bool       `db:"has_log"`
}

type LogLine struct {
	N    int    `db:"n"`
	Text string `db:"text"`
	Kind string `db:"kind"`
}

type Deployment struct {
	ID                  string          `db:"id"`
	Name                string          `db:"name"`
	Status              string          `db:"status"`
	ProjectID           string          `db:"project_id"`
	Environment         string          `db:"environment"`
	ItemID              string          `db:"item_id"`
	VersionID           string          `db:"version_id"`
	OwnerID             string          `db:"owner_id"`
	CreatedByRequestID  *string         `db:"created_by_request_id"`
	CreatedAt           time.Time       `db:"created_at"`
	LeaseExpiresAt      *time.Time      `db:"lease_expires_at"`
	LeaseExtensionsUsed int             `db:"lease_extensions_used"`
	LeaseExtensionsMax  *int            `db:"lease_extensions_max"`
	ExpiryAction        string          `db:"expiry_action"`
	SyncState           string          `db:"sync_state"`
	Inputs              json.RawMessage `db:"inputs"`
}

// DeploymentRow is a deployment joined with the names and counts the API shows.
type DeploymentRow struct {
	Deployment
	ProjectCode   string  `db:"project_code"`
	ProjectName   string  `db:"project_name"`
	ItemSlug      string  `db:"item_slug"`
	ItemName      string  `db:"item_name"`
	VersionNumber string  `db:"version_number"`
	OwnerName     string  `db:"owner_name"`
	OwnerEmail    string  `db:"owner_email"`
	VMCount       int     `db:"vm_count"`
	VMsPoweredOn  int     `db:"vms_powered_on"`
	PrimaryIP     *string `db:"primary_ip"`
	PrimaryFQDN   *string `db:"primary_fqdn"`
}

type Resource struct {
	ID           string          `db:"id"`
	DeploymentID string          `db:"deployment_id"`
	RequestID    *string         `db:"request_id"`
	Type         string          `db:"type"`
	Name         string          `db:"name"`
	ExternalID   *string         `db:"external_id"`
	SyncState    string          `db:"sync_state"`
	Properties   json.RawMessage `db:"properties"`
}

type InvCluster struct {
	Moref         string `db:"moref"`
	Name          string `db:"name"`
	Datacenter    string `db:"datacenter"`
	HostCount     int    `db:"host_count"`
	CPUTotalMHz   int64  `db:"cpu_total_mhz"`
	CPUUsedMHz    int64  `db:"cpu_used_mhz"`
	MemoryTotalMB int64  `db:"memory_total_mb"`
	MemoryUsedMB  int64  `db:"memory_used_mb"`
}

type InvDatastore struct {
	Moref      string   `db:"moref"`
	Name       string   `db:"name"`
	CapacityGB int64    `db:"capacity_gb"`
	FreeGB     int64    `db:"free_gb"`
	Clusters   []string `db:"clusters"`
}

type InvNetwork struct {
	Moref    string   `db:"moref"`
	Name     string   `db:"name"`
	Kind     string   `db:"kind"`
	Clusters []string `db:"clusters"`
}

type InvTemplate struct {
	Moref   string `db:"moref"`
	Name    string `db:"name"`
	GuestOS string `db:"guest_os"`
}

type InvVM struct {
	Moref         string   `db:"moref"`
	InstanceUUID  string   `db:"instance_uuid"`
	Name          string   `db:"name"`
	PowerState    string   `db:"power_state"`
	GuestState    string   `db:"guest_state"`
	IPAddress     string   `db:"ip_address"`
	Hostname      string   `db:"hostname"`
	GuestOS       string   `db:"guest_os"`
	CPUCount      int      `db:"cpu_count"`
	MemoryMB      int      `db:"memory_mb"`
	Cluster       string   `db:"cluster"`
	Host          string   `db:"host"`
	CPUPct        *float32 `db:"cpu_pct"`
	MemoryPct     *float32 `db:"memory_pct"`
	SnapshotCount int      `db:"snapshot_count"`
}

// Inventory is one full snapshot of a vCenter, written by the sync loop.
type Inventory struct {
	VCenter    string
	Clusters   []InvCluster
	Datastores []InvDatastore
	Networks   []InvNetwork
	Templates  []InvTemplate
	VMs        []InvVM
}

type Event struct {
	ID        int64           `db:"id"`
	Type      string          `db:"type"`
	ProjectID *string         `db:"project_id"`
	Topics    []string        `db:"topics"`
	Payload   json.RawMessage `db:"payload"`
	CreatedAt time.Time       `db:"created_at"`
}

// Day2Policy decides who may run which day-2 action, and whether it needs approval.
type Day2Policy struct {
	ID        string          `db:"id"`
	Name      string          `db:"name"`
	Reason    string          `db:"reason"`
	Priority  int             `db:"priority"`
	Actions   []string        `db:"actions"`
	Roles     []string        `db:"roles"`
	Condition string          `db:"condition"`
	Effect    string          `db:"effect"`
	Levels    json.RawMessage `db:"levels"`
	ProjectID *string         `db:"project_id"`
}
