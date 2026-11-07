package blueprint

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/restcall"
)

// Topology is what a requester sees of a blueprint: the part the canvas
// draws (ADR-0032), so the request form can show what the item builds. A VM
// item's components (how many, template, OS family, disks, adapters, kept
// apart), its networks, segments, security groups and rules; a composite's
// steps, what each runs, its condition and undo, and which earlier steps
// its bindings use. Nothing else: no guest settings (domain, join
// credential, administrator password, cloud-init), sizes, naming, tags,
// placement or AWX variables, a step's bindings only as the `steps.<key>`
// they name, a REST call (a poll's too) only as its method and what its URL
// and body read, and a wait for a person without who it names (a group,
// people) or what it says or offers. The CEL it keeps (a condition, a count, a
// disk's size, the adapters', groups', template and post-configuration
// bindings, a REST call's, a wait's time, a poll's condition) has its string
// literals masked (policy.MaskLiterals): what it reads, not what it compares
// it to. Lists keep their positions, so a pointer into
// the blueprint points at the same thing in its topology. Nil for the
// kinds the canvas doesn't draw (AWX, workflow) and for a blueprint that
// isn't JSON.
func Topology(raw json.RawMessage) map[string]any {
	var bp map[string]any
	if json.Unmarshal(raw, &bp) != nil {
		return nil
	}
	var s shape
	switch bp["kind"] {
	case KindVM:
		s = vmTopology
	case KindComposite:
		s = compositeTopology
	default:
		return nil
	}
	out, _ := s.keep(bp).(map[string]any)
	return out
}

// A shape says what of a JSON value to keep.
type shape interface{ keep(v any) any }

// asIs keeps the value whole.
type asIs struct{}

func (asIs) keep(v any) any { return v }

// fields keeps these keys of an object, each by its shape.
type fields map[string]shape

func (f fields) keep(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for k, s := range f {
		if x, ok := m[k]; ok {
			if y := s.keep(x); y != nil {
				out[k] = y
			}
		}
	}
	return out
}

// each keeps every item of a list by one shape, at its position.
type each struct{ of shape }

func (e each) keep(v any) any {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, len(l))
	for i, x := range l {
		out[i] = e.of.keep(x)
	}
	return out
}

// values keeps every key of an object, each value by one shape.
type values struct{ of shape }

func (s values) keep(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, x := range m {
		out[k] = s.of.keep(x)
	}
	return out
}

// cel keeps a CEL expression with its string literals masked
// (policy.MaskLiterals); lone keeps an expression that is only literals,
// the value the canvas draws by name. A number or a boolean is kept, as a
// list of strings is where lone (a value rather than CEL); anything else
// goes.
type cel struct{ lone bool }

func (c cel) keep(v any) any {
	switch x := v.(type) {
	case string:
		return policy.MaskLiterals(x, c.lone)
	case float64, bool:
		return x
	case []any:
		for _, e := range x {
			if _, ok := e.(string); !ok || !c.lone {
				return nil
			}
		}
		return x
	}
	return nil
}

// restURL keeps a REST call's URL as CEL's literals are kept: its text goes
// (the host, the path), and each ${expr} stays, its string literals masked.
type restURL struct{}

func (restURL) keep(v any) any {
	tpl, ok := v.(string)
	if !ok {
		return nil
	}
	parts, err := restcall.ParseURL(tpl)
	if err != nil {
		return policy.Masked
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Expr == "" {
			b.WriteString(policy.Masked)
			continue
		}
		b.WriteString("${" + policy.MaskLiterals(p.Expr, false) + "}")
	}
	return b.String()
}

var stepRef = regexp.MustCompile(`\bsteps\.[a-z][a-z0-9_]*`)

// stepRefs keeps of a step's bindings only the earlier steps they use:
// each binding that names one becomes those names ("steps.vm"), the others
// go. A binding's other literals may be anything, a password included.
type stepRefs struct{}

func (stepRefs) keep(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for k, x := range m {
		if s, ok := x.(string); ok {
			if refs := stepRef.FindAllString(s, -1); len(refs) > 0 {
				out[k] = strings.Join(refs, " ")
			}
		}
	}
	return out
}

var (
	// The bindings the canvas draws: adapters, groups chosen in the
	// request, the template and post-configuration.
	topologyBindings = fields{"network": cel{lone: true}, "groups": cel{lone: true}, "template": cel{lone: true},
		"post_config": cel{lone: true}}
	topologyGuest = fields{"os_family": asIs{}}
	topologyDisks = each{fields{"size_gb": cel{}, "storage_policy": asIs{}}}
	topologyNICs  = each{fields{"key": asIs{}, "network": cel{lone: true}, "ipam": asIs{}}}

	vmTopology = fields{
		"kind":          asIs{},
		"template":      asIs{},
		"library":       asIs{},
		"guest":         topologyGuest,
		"bindings":      topologyBindings,
		"awx_templates": values{fields{"title": asIs{}}},
		"disks":         topologyDisks,
		"nics":          topologyNICs,
		"groups":        asIs{},
		"segments":      each{fields{"key": asIs{}, "title": asIs{}}},
		"security_groups": each{fields{
			"key":   asIs{},
			"title": asIs{},
			"rules": each{fields{"from": asIs{}, "services": asIs{}, "action": asIs{}}},
		}},
		"components": each{fields{
			"key":           asIs{},
			"title":         asIs{},
			"count":         cel{},
			"template":      asIs{},
			"library":       asIs{},
			"guest":         topologyGuest,
			"bindings":      topologyBindings,
			"disks":         topologyDisks,
			"nics":          topologyNICs,
			"anti_affinity": asIs{},
			"groups":        asIs{},
		}},
	}

	// A REST call: its method, and what its URL and body read. Not its
	// secret's name, the statuses it accepts or what it keeps of the answer.
	topologyREST = fields{"method": asIs{}, "url": restURL{}, "body": cel{}}

	// A wait for a person: that it waits, whether the requester or owner is
	// asked, for how long and its form's keys, which later steps read anyway.
	// Who else it names, what it says and its form's texts stay out.
	topologyAsk = fields{"requester": asIs{}, "owner": asIs{}, "timeout": asIs{}, "on_timeout": asIs{},
		"form": each{fields{"key": asIs{}}}}

	// A wait for a time: how long, or what its time reads.
	topologyWait = fields{"for": asIs{}, "until": cel{}}

	// A poll: its call as a REST call's, what its condition reads, how often,
	// for how long and what then.
	topologyPoll = fields{"rest": topologyREST, "until": cel{}, "every": asIs{}, "timeout": asIs{}, "on_timeout": asIs{}}

	topologyStep = fields{
		"key":      asIs{},
		"title":    asIs{},
		"item":     asIs{},
		"awx":      fields{"job_template": asIs{}, "title": asIs{}},
		"workflow": asIs{},
		"rest":     topologyREST,
		"ask":      topologyAsk,
		"wait":     topologyWait,
		"poll":     topologyPoll,
		"when":     cel{},
		"with":     stepRefs{},
		"undo":     fields{"job_template": asIs{}, "workflow": asIs{}, "rest": topologyREST, "with": stepRefs{}},
	}
	compositeTopology = fields{
		"kind":                asIs{},
		"rollback_on_failure": asIs{},
		"steps":               each{topologyStep},
	}
)

func init() {
	// A parallel group lists steps (and the canvas says so of one that nests).
	topologyStep["parallel"] = each{topologyStep}
}
