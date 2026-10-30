// Package nsx reads and writes NSX through its Policy API (/policy/api/v1,
// NSX 4.x and NSX-T 3.x): the Tier-1 gateways, segments and groups rvav
// refers to, and the segments, groups, security policies and VM tags it
// makes for deployments (ADR-0028). FakeServer answers the same calls, for
// the tests and the compose stack.
package nsx

import "slices"

// Tag is an NSX tag: a scope and a value.
type Tag struct {
	Scope string `json:"scope,omitempty"`
	Tag   string `json:"tag"`
}

// Tier1 is a Tier-1 gateway; segments connect to it.
type Tier1 struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path,omitempty"`
	Tags        []Tag  `json:"tags,omitempty"`
}

// SegmentSubnet is a segment's subnet: its gateway address in CIDR form
// (10.31.40.1/24).
type SegmentSubnet struct {
	GatewayAddress string `json:"gateway_address"`
	Network        string `json:"network,omitempty"`
}

// Segment is a layer-2 network; in vCenter it is a port group of its name.
type Segment struct {
	ID                string          `json:"id"`
	DisplayName       string          `json:"display_name"`
	Path              string          `json:"path,omitempty"`
	ConnectivityPath  string          `json:"connectivity_path,omitempty"` // its Tier-1
	TransportZonePath string          `json:"transport_zone_path,omitempty"`
	Subnets           []SegmentSubnet `json:"subnets,omitempty"`
	Tags              []Tag           `json:"tags,omitempty"`
}

// Condition is one part of a group's expression: a condition on a member's
// tag or name, or the operator joining two conditions.
type Condition struct {
	ResourceType        string `json:"resource_type"` // Condition, ConjunctionOperator
	ID                  string `json:"id,omitempty"`
	MemberType          string `json:"member_type,omitempty"` // VirtualMachine, Segment...
	Key                 string `json:"key,omitempty"`         // Tag, Name, ComputerName
	Operator            string `json:"operator,omitempty"`    // EQUALS, CONTAINS, STARTSWITH...
	Value               string `json:"value,omitempty"`       // for a tag: scope|tag
	ConjunctionOperator string `json:"conjunction_operator,omitempty"`
}

// Group is a security group, whose members its expression selects.
type Group struct {
	ID          string      `json:"id"`
	DisplayName string      `json:"display_name"`
	Path        string      `json:"path,omitempty"`
	Expression  []Condition `json:"expression,omitempty"`
	Tags        []Tag       `json:"tags,omitempty"`
}

// Rule is a distributed firewall rule.
type Rule struct {
	ID                string   `json:"id"`
	DisplayName       string   `json:"display_name,omitempty"`
	SourceGroups      []string `json:"source_groups"`      // group paths, or ANY
	DestinationGroups []string `json:"destination_groups"` // group paths, or ANY
	Services          []string `json:"services"`           // service paths, or ANY
	Scope             []string `json:"scope,omitempty"`    // where it is enforced: group paths, or ANY
	Action            string   `json:"action"`             // ALLOW, DROP, REJECT
	Direction         string   `json:"direction,omitempty"`
	SequenceNumber    int      `json:"sequence_number,omitempty"`
	Disabled          bool     `json:"disabled,omitempty"`
	Logged            bool     `json:"logged,omitempty"`
}

// SecurityPolicy is a section of distributed firewall rules.
type SecurityPolicy struct {
	ID             string   `json:"id"`
	DisplayName    string   `json:"display_name"`
	Path           string   `json:"path,omitempty"`
	Category       string   `json:"category,omitempty"` // Application, Environment...
	Scope          []string `json:"scope,omitempty"`
	SequenceNumber int      `json:"sequence_number,omitempty"`
	Rules          []Rule   `json:"rules,omitempty"`
	Tags           []Tag    `json:"tags,omitempty"`
}

// Service is a protocol and ports firewall rules name (HTTPS, SSH...).
type Service struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path,omitempty"`
}

// TransportZone is where segments can be: overlay or VLAN.
type TransportZone struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path,omitempty"`
	Type        string `json:"tz_type"` // OVERLAY_STANDARD, VLAN_BACKED...
}

// VM is a virtual machine as NSX knows it from vCenter: by its instance
// UUID (external_id), with its tags.
type VM struct {
	ExternalID  string `json:"external_id"`
	DisplayName string `json:"display_name"`
	Tags        []Tag  `json:"tags,omitempty"`
}

// Path prefixes of the objects rvav uses.
const (
	Tier1Path    = "/infra/tier-1s/"
	SegmentPath  = "/infra/segments/"
	GroupPath    = "/infra/domains/default/groups/"
	PolicyPath   = "/infra/domains/default/security-policies/"
	ServicePath  = "/infra/services/"
	OwnerScope   = "rvav"            // the tag scope of what rvav makes: its instance
	DeployScope  = "rvav-deployment" // and the deployment it is for
	ConditionTag = "Tag"
)

// Owned says whether rvav's instance made it: only those does it delete.
func Owned(tags []Tag, instance string) bool {
	return slices.ContainsFunc(tags, func(t Tag) bool { return t.Scope == OwnerScope && t.Tag == instance })
}

// TagCondition selects VMs carrying a tag.
func TagCondition(t Tag) Condition {
	return Condition{ResourceType: "Condition", MemberType: "VirtualMachine", Key: ConditionTag, Operator: "EQUALS",
		Value: t.Scope + "|" + t.Tag}
}
