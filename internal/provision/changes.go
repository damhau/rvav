package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/store"
)

// Blocking subscriptions to a VM's topics (ADR-0054): compute.allocation.pre
// and network.configure are asked about each VM the request hasn't built,
// after its event, and may stop the request or change the VM: its name and
// placement, then its adapters' networks and addresses. rvav checks a
// change again before building, as it checked the request when submitted.

// MachineAnswer is what a blocking answer's machine changes on its VM.
type MachineAnswer struct {
	Name      *string          `json:"name,omitempty"`
	Placement *PlacementAnswer `json:"placement,omitempty"`
	Networks  []NetworkAnswer  `json:"networks,omitempty"`
}

// PlacementAnswer moves a VM: what it doesn't name stays.
type PlacementAnswer struct {
	Cluster      *string `json:"cluster,omitempty"`
	Datastore    *string `json:"datastore,omitempty"`
	ResourcePool *string `json:"resource_pool,omitempty"`
	Folder       *string `json:"folder,omitempty"`
}

// NetworkAnswer changes one adapter: 0 is the VM's first, as in the
// event's machine.networks.
type NetworkAnswer struct {
	Index   *int    `json:"index"`
	Network *string `json:"network,omitempty"`
	Address *string `json:"address,omitempty"`
}

// ParseMachineAnswer reads an answer's machine for a topic, and says what
// is wrong with it.
func ParseMachineAnswer(topic string, raw json.RawMessage) (MachineAnswer, string) {
	var m MachineAnswer
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, "its machine isn't what rvav reads (" + err.Error() + ")."
	}
	switch topic {
	case outbox.ComputeAllocationPre:
		if m.Networks != nil {
			return m, "a VM's networks and addresses change on network.configure, not compute.allocation.pre."
		}
	case outbox.NetworkConfigure:
		if m.Name != nil || m.Placement != nil {
			return m, "a VM's name and placement change on compute.allocation.pre, not network.configure."
		}
		seen := map[int]bool{}
		for _, n := range m.Networks {
			if n.Index == nil {
				return m, "each network it changes needs its index: 0 for the VM's first adapter."
			}
			if seen[*n.Index] {
				return m, fmt.Sprintf("it changes adapter %d twice.", *n.Index)
			}
			seen[*n.Index] = true
		}
	default:
		return m, "only compute.allocation.pre and network.configure subscriptions may change a VM."
	}
	return m, ""
}

// MachineChange is what a blocking subscription's answer asks of a VM the
// request hasn't built.
type MachineChange struct {
	RequestID string
	Topic     string
	// Step is the composite step the VM is built by, "" for the plan's.
	Step   string
	VM     VMUnit
	Answer MachineAnswer
}

// MachineChanger checks a change to a VM again, as the request was checked
// when submitted, and applies it to the request's plan and steps; it
// returns the VM changed and what changed, for its step. A change it
// refuses is a *RefusedChange.
type MachineChanger interface {
	ChangeMachine(ctx context.Context, ch MachineChange) (VMUnit, []string, error)
}

// RefusedChange is a change rvav refuses, and why.
type RefusedChange struct{ Reason string }

func (e *RefusedChange) Error() string { return e.Reason }

// checksKey is the step of the blocking subscriptions to a VM topic: the
// plan's, or under the composite step building the VMs.
func checksKey(parent, topic string) string {
	key := "checks:" + topic
	if parent != "" {
		key = parent + "/" + key
	}
	return key
}

// machineChecks asks the blocking subscriptions to a VM topic about each
// of these VMs, in priority order, and gives a VM what they changed;
// versioned, so workflows that started without them replay.
func machineChecks(ctx workflow.Context, info Info, topic, parent string, vms []VMUnit) (string, error) {
	key := checksKey(parent, topic)
	if len(vms) == 0 || info.done(key) || workflow.GetVersion(ctx, "machine-checks", workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return "", nil
	}
	var a *Activities
	var list []MachineCheck
	in := MachineChecksInput{RequestID: info.RequestID, ProjectID: info.ProjectID, Topic: topic, Parent: parent, VMs: vms}
	if err := workflow.ExecuteActivity(ctx, a.MachineChecks, in).Get(ctx, &list); err != nil {
		return key, err
	}
	if len(list) == 0 {
		return "", nil
	}
	for _, c := range list {
		// Its own timeout, and its recovery's, each tried once more after a
		// fast failure; not retried by Temporal, which would ask again.
		o := activityOptions
		o.StartToCloseTimeout = 4*time.Duration(c.Timeout)*time.Second + time.Minute
		o.HeartbeatTimeout = 0
		o.RetryPolicy = &temporal.RetryPolicy{MaximumAttempts: 1}
		var out MachineCheckOutcome
		run := MachineCheckInput{RequestID: info.RequestID, ProjectID: info.ProjectID, Topic: topic, Parent: parent, Check: c, VM: vms[c.VM]}
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, o), a.RunMachineCheck, run).Get(ctx, &out); err != nil {
			_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "failed", errMessage(err)).Get(ctx, nil)
			return c.Key, err
		}
		if out.Stop {
			return c.Key, temporal.NewNonRetryableApplicationError(out.Reason, "Permanent", nil)
		}
		if out.VM != nil {
			vms[c.VM] = *out.VM
		}
	}
	return "", workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "done", "").Get(ctx, nil)
}

// MachineChecksInput names the VMs a topic's blocking subscriptions are
// asked about.
type MachineChecksInput struct {
	RequestID string   `json:"request_id"`
	ProjectID string   `json:"project_id"`
	Topic     string   `json:"topic"`
	Parent    string   `json:"parent,omitempty"`
	VMs       []VMUnit `json:"vms"`
}

// MachineCheck is a blocking subscription to ask about one VM, the VMs'
// VM-th, under its step Key.
type MachineCheck struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Timeout int    `json:"timeout_seconds"`
	EventID string `json:"event_id"`
	VM      int    `json:"vm"`
	Key     string `json:"key"`
}

// MachineCheckInput is one check, with the VM as earlier checks left it.
type MachineCheckInput struct {
	RequestID string       `json:"request_id"`
	ProjectID string       `json:"project_id"`
	Topic     string       `json:"topic"`
	Parent    string       `json:"parent,omitempty"`
	Check     MachineCheck `json:"check"`
	VM        VMUnit       `json:"vm"`
}

// MachineCheckOutcome says whether the request stops, and the VM as the
// subscription changed it (nil when it didn't).
type MachineCheckOutcome struct {
	Stop   bool    `json:"stop"`
	Reason string  `json:"reason,omitempty"`
	VM     *VMUnit `json:"vm,omitempty"`
}

// MachineChecks lists the blocking subscriptions to ask about each VM not
// built yet, whose condition is true for its event, by priority, and adds
// their steps. One that let the VM go on in an earlier run of the request
// isn't asked again: what it changed is in the plan.
func (a *Activities) MachineChecks(ctx context.Context, in MachineChecksInput) ([]MachineCheck, error) {
	key := checksKey(in.Parent, in.Topic)
	subs, err := a.Store.ActiveSubscriptions(ctx, in.Topic, true)
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, a.answeredBefore(ctx, in, key)
	}
	if a.Subscriptions == nil {
		a.Log.Warn("blocking subscriptions not checked: no dispatcher in this worker", "request", in.RequestID, "topic", in.Topic)
		return nil, nil
	}
	built, err := a.Store.ResourcesForRequest(ctx, in.RequestID)
	if err != nil {
		return nil, err
	}
	var out []MachineCheck
	for i, vm := range in.VMs {
		if hasVM(built, vm) {
			continue
		}
		eventID, err := a.Store.EventIDByDedupe(ctx, machineKey(ctx, in.RequestID, in.Topic, vm.Name))
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		_, payload, err := a.eventPayload(ctx, eventID)
		if err != nil {
			return nil, err
		}
		for _, s := range subs {
			match, err := dispatch.Matches(s.Condition, payload)
			if err != nil {
				// A condition that can't be evaluated applies: a check is
				// never skipped by mistake.
				a.Log.Warn("blocking subscription condition", "subscription", s.ID, "err", err)
				match = true
			}
			if !match {
				continue
			}
			answered, err := a.Store.AnsweredMachineCheck(ctx, in.RequestID, s.ID, in.Topic, vm.DeploymentID, vm.Component, vm.Index)
			if err != nil {
				return nil, err
			}
			if !answered {
				out = append(out, MachineCheck{ID: s.ID, Name: s.Name, Timeout: s.TimeoutSeconds, EventID: eventID, VM: i,
					Key: key + "/" + s.ID + "/" + strconv.Itoa(i)})
			}
		}
	}
	if len(out) == 0 {
		return nil, a.answeredBefore(ctx, in, key)
	}
	title, detail := "Network checks", "Subscriptions to network.configure that may change the VMs' adapters or stop the request"
	if in.Topic == outbox.ComputeAllocationPre {
		title, detail = "Allocation checks", "Subscriptions to compute.allocation.pre that may rename or move the VMs, or stop the request"
	}
	var st store.Step
	if in.Parent == "" {
		st, err = a.Store.InsertStepBeforePending(ctx, a.Store.Pool, in.RequestID, key, title, detail, "rvav")
	} else {
		st, err = a.Store.InsertChildBeforePending(ctx, a.Store.Pool, in.RequestID, in.Parent, key, title, detail)
	}
	if err != nil {
		return nil, err
	}
	a.Events.Step(ctx, in.ProjectID, in.RequestID, st)
	parent := JobInput{RequestID: in.RequestID, ProjectID: in.ProjectID, StepKey: key}
	for _, c := range out {
		a.child(ctx, parent, c.Key, c.Name+" · "+in.VMs[c.VM].Name, "pending", nil)
	}
	return out, nil
}

// answeredBefore closes the checks step a retry planned again when
// nothing is left to ask: what the subscriptions changed is in the plan.
func (a *Activities) answeredBefore(ctx context.Context, in MachineChecksInput, key string) error {
	st, err := a.Store.GetStepByKey(ctx, in.RequestID, key)
	if errors.Is(err, store.ErrNotFound) || err == nil && st.Status == "done" {
		return nil
	}
	if err != nil {
		return err
	}
	why := "Its subscriptions answered when the request ran before: what they changed is kept."
	a.update(ctx, in.RequestID, in.ProjectID, key, store.StepUpdate{Status: "done", Detail: &why})
	return nil
}

// RunMachineCheck asks one blocking subscription about one VM, applies
// what its answer changes once rvav checked it, and says whether the
// request stops.
func (a *Activities) RunMachineCheck(ctx context.Context, in MachineCheckInput) (MachineCheckOutcome, error) {
	var out MachineCheckOutcome
	sub, err := a.Store.GetSubscription(ctx, in.Check.ID)
	if err != nil {
		return out, err
	}
	_, payload, err := a.eventPayload(ctx, in.Check.EventID)
	if err != nil {
		return out, err
	}
	parent := JobInput{RequestID: in.RequestID, ProjectID: in.ProjectID, StepKey: checksKey(in.Parent, in.Topic)}
	a.child(ctx, parent, in.Check.Key, "", "running", nil)
	res, err := a.ask(ctx, asking{sub: sub, eventID: in.Check.EventID, payload: withMachine(payload, in.VM), in: parent, key: in.Check.Key,
		accept: func(res dispatch.Result) (taken, error) {
			if len(res.Properties) > 0 {
				return taken{problem: "Only provisioning.pre subscriptions may set properties."}, nil
			}
			if len(res.Machine) == 0 {
				return taken{}, nil
			}
			answer, problem := ParseMachineAnswer(in.Topic, res.Machine)
			if problem != "" {
				return taken{problem: problem, refused: true}, nil
			}
			if a.Changer == nil {
				return taken{}, errors.New("this worker can't change a request's VMs")
			}
			vm, did, err := a.Changer.ChangeMachine(ctx, MachineChange{RequestID: in.RequestID, Topic: in.Topic, Step: in.Parent,
				VM: in.VM, Answer: answer})
			var refused *RefusedChange
			if errors.As(err, &refused) {
				return taken{problem: refused.Reason, refused: true}, nil
			}
			if err != nil {
				return taken{}, err
			}
			out.VM = &vm
			changes, _ := json.Marshal(answer)
			detail := "Went on."
			if len(did) > 0 {
				detail = "Went on, " + strings.Join(did, ", ") + "."
			}
			return taken{detail: detail, changes: changes}, nil
		}})
	if err != nil {
		return out, err
	}
	if res.stop {
		out.Stop, out.Reason, out.VM = true, res.reason, nil
		a.update(ctx, in.RequestID, in.ProjectID, parent.StepKey, store.StepUpdate{Status: "failed", Error: &res.reason})
	}
	return out, nil
}

// withMachine is a VM's event as a subscription gets it: with what
// earlier subscriptions changed on the VM.
func withMachine(payload json.RawMessage, vm VMUnit) json.RawMessage {
	var ev map[string]any
	if json.Unmarshal(payload, &ev) != nil {
		return payload
	}
	data, _ := ev["data"].(map[string]any)
	if data == nil {
		return payload
	}
	data["machine"] = machineOf(vm, vm.chosenAddresses(), "", "")
	b, err := json.Marshal(ev)
	if err != nil {
		return payload
	}
	return b
}

// chosenAddresses are the addresses subscriptions chose for the VM's
// adapters, by key ("" for its first); nil when none did.
func (vm VMUnit) chosenAddresses() map[string]string {
	var out map[string]string
	set := func(k, v string) {
		if v == "" {
			return
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	set("", vm.Address)
	for _, n := range vm.NICs {
		set(n.Key, n.Address)
	}
	return out
}
