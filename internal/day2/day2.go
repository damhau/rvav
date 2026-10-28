// Package day2 is the catalogue of native day-2 actions (ADR-0013): what
// each action is called, when it applies to a resource as it is now, the
// form it asks for, and the operations the request runs.
package day2

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
)

// Action groups, in menu order.
const (
	GroupPower         = "power"
	GroupConfiguration = "configuration"
	GroupSnapshots     = "snapshots"
	GroupAccess        = "access"
	GroupLifecycle     = "lifecycle"
	// GroupAutomation holds the actions backed by AWX templates.
	GroupAutomation = "automation"
)

// Target is the resource an action runs on, as rvav last saw it. Actions
// on the whole deployment have the type "deployment", no resource ID, the
// deployment's name, and its resources.
type Target struct {
	ResourceID   string
	ResourceName string
	Type         string
	VM           api.VmProperties
	Resources    []Resource
	OwnerID      string
	Lease        Lease
}

// Lease is the deployment's lease now.
type Lease struct {
	ExpiresAt      *time.Time
	ExtensionsUsed int
	// ExpiredAt is when its expiry powered the VMs off (the grace period runs).
	ExpiredAt *time.Time
}

// Resource is one resource of a deployment.
type Resource struct {
	ID, Type, Name string
	VM             api.VmProperties
}

// DeploymentType is the target type of actions on a whole deployment.
const DeploymentType = "deployment"

// Choices are what the project and environment allow the target, for forms.
type Choices struct {
	// Networks the VM may be connected to.
	Networks []string
	// Environment is the deployment's environment name, for messages.
	Environment string
	// SnapshotMaxCount and SnapshotMaxAgeDays are the snapshot retention
	// (age 0: kept until deleted).
	SnapshotMaxCount   int
	SnapshotMaxAgeDays int
	// Members are who may own the deployment: the project's members.
	Members []api.Option
	// Projects the deployment may move to; disabled ones say why not.
	Projects []api.Option
	// The lease policy that applies (ADR-0014).
	LeaseDefaultDays, LeaseMaxDays *int
	LeaseMaxExtensions             int
}

// Op is one step of a day-2 request. The workflow runs Op with Args.
type Op struct {
	Key    string         `json:"key"`
	Title  string         `json:"title"`
	Detail string         `json:"detail,omitempty"`
	System string         `json:"system"`
	Op     string         `json:"op"`
	Args   map[string]any `json:"args,omitempty"`
}

// Action is a native day-2 action.
type Action struct {
	Key   string
	Name  string
	Group string
	// Types are the resource types it applies to.
	Types []string
	// Condition, a CEL expression over the day-2 policy variables, limits
	// where the action is offered ("": everywhere). Native actions use
	// Unavailable instead.
	Condition string
	// Projects, when set, are the only projects it is offered in.
	Projects    []string
	Destructive bool
	// Confirm is the message of the confirmation dialog, if any.
	Confirm string
	// TypeName asks the user to type the resource name to confirm.
	TypeName bool
	// Unavailable says why the action can't run on the target now ("" when it can).
	Unavailable func(t Target, c Choices) string
	// Form is the form the action asks for; nil when it has none. Its JSON
	// Schema is checked before Validate.
	Form func(t Target, c Choices) *api.ActionForm
	// Validate checks what the schema can't, such as a change to nothing.
	Validate func(t Target, inputs map[string]any) []api.FieldError
	// Ops are the steps of the request for these inputs.
	Ops func(t Target, inputs map[string]any) []Op
	// During and After are the deployment's status while the action runs
	// (default updating) and once it succeeded (default ready).
	During, After string
	// OnFailed: the action may run on a failed deployment.
	OnFailed bool
	// Direct actions are not requests: the UI calls their own endpoint
	// (the console), and only the policy decides.
	Direct bool
	// Hidden actions are not offered: only rvav runs them (lease expiry).
	Hidden bool
	// Approval says why the action needs approval on top of the day-2
	// policies ("" when it doesn't); inputs is nil when listing actions.
	Approval func(t Target, c Choices, inputs map[string]any) string
}

func (a *Action) AppliesTo(typ string) bool { return slices.Contains(a.Types, typ) }

var catalogue []*Action

func register(a *Action) { catalogue = append(catalogue, a) }

// All lists the actions in menu order.
func All() []*Action {
	return Sorted(catalogue)
}

// Sorted puts actions in menu order: by group, then as given.
func Sorted(actions []*Action) []*Action {
	order := map[string]int{GroupPower: 0, GroupConfiguration: 1, GroupSnapshots: 2, GroupAutomation: 3, GroupAccess: 4, GroupLifecycle: 5}
	out := slices.Clone(actions)
	slices.SortStableFunc(out, func(a, b *Action) int { return order[a.Group] - order[b.Group] })
	return out
}

// Get returns the action with this key, or nil.
func Get(key string) *Action {
	for _, a := range catalogue {
		if a.Key == key {
			return a
		}
	}
	return nil
}

// Name is the display name of an action key: an AWX action's is made from
// its template's (awx.install-monitoring-agent is "Install monitoring
// agent"), anything else unknown is the key itself.
func Name(key string) string {
	if a := Get(key); a != nil {
		return a.Name
	}
	if tpl, ok := strings.CutPrefix(key, "awx."); ok {
		if name := strings.ReplaceAll(strings.TrimSuffix(tpl, "-workflow"), "-", " "); name != "" {
			return strings.ToUpper(name[:1]) + name[1:] // keys are ASCII
		}
	}
	return key
}

// VM reads the VM properties stored on a resource.
func VM(properties json.RawMessage) api.VmProperties {
	var p struct {
		VM api.VmProperties `json:"vm"`
	}
	_ = json.Unmarshal(properties, &p)
	return p.VM
}

const vmType = "vsphere.vm"

func vcenter(key, title, op string, args map[string]any) Op {
	return Op{Key: key, Title: title, System: "vcenter", Op: op, Args: args}
}
