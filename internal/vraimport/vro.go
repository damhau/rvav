package vraimport

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// The vRO triage (P2.4): each workflow and action sorted into ADR-0007's
// groups, with what uses it and why, for people to decide.

// The groups.
const (
	GroupNative   = "A" // built-in rvav steps, a composite item or a value source
	GroupAWX      = "B" // an AWX job template
	GroupTemporal = "C" // a custom Temporal workflow
	GroupDrop     = "D" // nothing
)

// Unused for this long, a workflow nothing uses is suggested for D.
const staleAfter = 180 * 24 * time.Hour

// Use is something in vRA that runs a workflow or an action.
type Use struct {
	Kind   string `json:"kind"` // catalog_item, subscription, day2_action, form_field, workflow, action
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	// Disabled: it can't run (a disabled subscription).
	Disabled bool `json:"disabled,omitempty"`
}

// TriageItem is one workflow or action: what it is, what uses it, and its
// suggested group with the reasons.
type TriageItem struct {
	Kind     string         `json:"kind"` // workflow, action, abx_action
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Category string         `json:"category,omitempty"` // a workflow's folder, an action's module
	Inputs   []vra.VROParam `json:"inputs,omitempty"`
	UsedBy   []Use          `json:"used_by"`
	Runs     int            `json:"runs"`
	LastRun  *time.Time     `json:"last_run,omitempty"`
	// Systems it reaches: vCenter, Active Directory, REST, SQL...
	Systems []string `json:"systems"`
	Group   string   `json:"group"`
	Becomes string   `json:"becomes"`
	Reasons []string `json:"reasons"`
}

// Triage is the report.
type Triage struct {
	ReadAt time.Time    `json:"read_at"`
	Items  []TriageItem `json:"items"`
	// Groups counts the items in each group.
	Groups map[string]int `json:"groups"`
	// Library: vRO's own workflows and actions, left out (vRO ships them;
	// nobody migrates them).
	Library int `json:"library"`
}

var becomes = map[string]string{
	GroupNative:   "Built-in rvav steps or a composite item",
	GroupAWX:      "An AWX job template",
	GroupTemporal: "A custom Temporal workflow",
	GroupDrop:     "Nothing: retire it",
}

// What vRO's plugins and scripting objects reach, and the Ansible content
// that does the same.
var systems = []struct {
	Name    string
	Types   []string // input and attribute type prefixes
	Script  *regexp.Regexp
	Ansible string
}{
	{"vCenter", []string{"VC:"}, regexp.MustCompile(`\bVcPlugin\b|\bVc[A-Z]\w+|\.sdkConnection\b|_Task\(|\bvm\.(clone|reconfig|power|migrate|relocate)`), ""},
	{"Active Directory", []string{"AD:"}, regexp.MustCompile(`\bActiveDirectory\b|\bAD_[A-Z]\w+`), "microsoft.ad"},
	{"REST", []string{"REST:"}, regexp.MustCompile(`\bRESTHost(Manager)?\b|\bRESTRequest\b|\bHTTPRESTHost\b`), "ansible.builtin.uri"},
	{"SOAP", []string{"SOAP:"}, regexp.MustCompile(`\bSOAPHost(Manager)?\b`), "ansible.builtin.uri"},
	{"SQL", []string{"SQL:", "JDBC"}, regexp.MustCompile(`\bSQLDatabase(Manager)?\b|\bJDBCConnection\b`), "the database's collection (community.postgresql, community.mysql...)"},
	{"PowerShell", []string{"PowerShell:"}, regexp.MustCompile(`\bPowerShellHost(Manager)?\b|\bPowerShell\w*\b`), "ansible.windows"},
	{"SSH", []string{"SSH:"}, regexp.MustCompile(`\bSSHSession\b|\bSSHCommand\b`), "ansible.builtin (over SSH)"},
	{"Mail", []string{"Mail:"}, regexp.MustCompile(`\bEmailMessage\b|\bMailClient\b`), "community.general.mail (or rvav's notifications)"},
	{"NSX", []string{"NSX:", "NSXT:"}, regexp.MustCompile(`\bNSX\w*\b`), "vmware.ansible_for_nsxt"},
	{"vRA", []string{"VRA:", "vCACCAFE:", "VRA8:"}, regexp.MustCompile(`\bVraHost\w*\b|\bvCACCAFE\w*\b|\bVraEntitiesFinder\b`), ""},
}

// System.getModule("com.corp.cmdb").ciPayload(...)
var moduleCall = regexp.MustCompile(`System\.getModule\(\s*["']([\w.]+)["']\s*\)\s*\.\s*(\w+)\s*\(`)

// isLibrary: vRO ships it.
func isLibrary(category string) bool { return vra.IsVROLibrary(category) }

// wfContent is the part of a workflow's content the triage reads.
type wfContent struct {
	Items []struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Linked string `json:"linked-workflow-id"`
		Script struct {
			Value string `json:"value"`
		} `json:"script"`
	} `json:"workflow-item"`
	Attrib []struct {
		Type string `json:"type"`
	} `json:"attrib"`
}

// TriageVRO sorts the estate's vRO workflows and actions, and the ABX
// actions vRA runs, into ADR-0007's groups.
func TriageVRO(e *vra.Estate) Triage {
	now := e.ReadAt
	if now.IsZero() {
		now = time.Now()
	}
	t := Triage{ReadAt: now, Groups: map[string]int{}, Items: []TriageItem{}}
	uses := map[string][]Use{} // "workflow:ID", "action:module/name", "abx:ID" → uses
	add := func(key string, u Use) { uses[key] = append(uses[key], u) }

	for _, ci := range e.CatalogItems {
		switch ci.Type.ID {
		case "com.vmw.vro.workflow":
			add("workflow:"+e.SourceOf(ci), Use{Kind: "catalog_item", Name: ci.Name, Detail: projectsOf(e, e.SharedWith(ci))})
		case "com.vmw.abx.actions":
			add("abx:"+e.SourceOf(ci), Use{Kind: "catalog_item", Name: ci.Name, Detail: projectsOf(e, e.SharedWith(ci))})
		}
	}
	for _, s := range e.Subscriptions {
		detail := s.EventTopicID
		if ev := rvavEvent(s.EventTopicID); ev != "" {
			detail += ", " + ev + " in rvav"
		}
		if s.Blocking {
			detail += ", blocking"
		}
		u := Use{Kind: "subscription", Name: s.Name, Detail: detail, Disabled: s.Disabled}
		switch s.RunnableType {
		case "extensibility.vro":
			add("workflow:"+s.RunnableID, u)
		case "extensibility.abx":
			add("abx:"+s.RunnableID, u)
		}
	}
	for _, ra := range e.ResourceActions {
		name := ra.DisplayName
		if name == "" {
			name = ra.Name
		}
		u := Use{Kind: "day2_action", Name: name, Detail: "on " + ra.ResourceType, Disabled: ra.Status != "" && ra.Status != "RELEASED"}
		switch ra.RunnableItem.Type {
		case "vro.workflow":
			add("workflow:"+ra.RunnableItem.ID, u)
		case "abx.action":
			add("abx:"+ra.RunnableItem.ID, u)
		}
	}
	for _, f := range e.Forms {
		var form struct {
			Schema map[string]struct {
				Label     string          `json:"label"`
				ValueList json.RawMessage `json:"valueList"`
				Default   json.RawMessage `json:"default"`
			} `json:"schema"`
		}
		if json.Unmarshal([]byte(f.Form), &form) != nil {
			continue
		}
		for id, field := range form.Schema {
			for _, raw := range []json.RawMessage{field.ValueList, field.Default} {
				var src struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				}
				if json.Unmarshal(raw, &src) == nil && src.Type == "scriptAction" && src.ID != "" {
					label := field.Label
					if label == "" {
						label = id
					}
					add("action:"+src.ID, Use{Kind: "form_field", Name: label, Detail: "the " + f.Name + " form's " + id})
				}
			}
		}
	}

	// The workflows: what they reach, what they call.
	type wfInfo struct {
		systems  map[string]bool
		calls    []string // actions, module/name
		linked   []string // workflows
		human    bool     // user interactions
		waits    bool     // timers, events
		decision int
		content  string
	}
	infos := map[string]*wfInfo{}
	names := map[string]string{}
	for _, w := range e.Workflows {
		names[w.ID] = w.Name
		in := &wfInfo{systems: map[string]bool{}}
		var c wfContent
		_ = json.Unmarshal([]byte(w.Content), &c)
		var scripts []string
		types := []string{}
		for _, p := range w.Inputs {
			types = append(types, p.Type)
		}
		for _, a := range c.Attrib {
			types = append(types, a.Type)
		}
		for _, it := range c.Items {
			switch it.Type {
			case "task", "custom-condition":
				scripts = append(scripts, it.Script.Value)
			case "link":
				if it.Linked != "" {
					in.linked = append(in.linked, it.Linked)
				}
			case "input":
				in.human = true
			case "waiting-timer", "waiting-event", "custom-event", "waiting-signal":
				in.waits = true
			case "decision", "switch":
				in.decision++
			}
		}
		script := strings.Join(scripts, "\n")
		reach(in.systems, types, script)
		for _, m := range moduleCall.FindAllStringSubmatch(script, -1) {
			in.calls = append(in.calls, m[1]+"/"+m[2])
		}
		in.content = normalContent(w.Content, w.ID, w.Name)
		infos[w.ID] = in
		for _, l := range in.linked {
			add("workflow:"+l, Use{Kind: "workflow", Name: w.Name})
		}
		for _, a := range slices.Compact(slices.Sorted(slices.Values(in.calls))) {
			add("action:"+a, Use{Kind: "workflow", Name: w.Name})
		}
	}
	actionCalls := map[string][]string{}
	for _, a := range e.Actions {
		for _, m := range moduleCall.FindAllStringSubmatch(a.Script, -1) {
			fqn := m[1] + "/" + m[2]
			actionCalls[a.FQN()] = append(actionCalls[a.FQN()], fqn)
			add("action:"+fqn, Use{Kind: "action", Name: a.FQN()})
		}
	}
	actionSystems := func(fqn string) map[string]bool {
		out := map[string]bool{}
		seen := map[string]bool{}
		var walk func(string)
		walk = func(f string) {
			if seen[f] {
				return
			}
			seen[f] = true
			i := slices.IndexFunc(e.Actions, func(a vra.VROAction) bool { return a.FQN() == f })
			if i < 0 {
				return
			}
			a := e.Actions[i]
			types := []string{a.OutputType}
			for _, p := range a.Inputs {
				types = append(types, p.Type)
			}
			reach(out, types, a.Script)
			for _, c := range actionCalls[f] {
				walk(c)
			}
		}
		walk(fqn)
		return out
	}
	// A workflow reaches what the workflows and actions it calls reach.
	var wfSystems func(id string, seen map[string]bool) (map[string]bool, bool, bool)
	wfSystems = func(id string, seen map[string]bool) (map[string]bool, bool, bool) {
		out := map[string]bool{}
		in, ok := infos[id]
		if !ok || seen[id] {
			return out, false, false
		}
		seen[id] = true
		human, waits := in.human, in.waits
		for k := range in.systems {
			out[k] = true
		}
		for _, c := range in.calls {
			for k := range actionSystems(c) {
				out[k] = true
			}
		}
		for _, l := range in.linked {
			s, h, w := wfSystems(l, seen)
			for k := range s {
				out[k] = true
			}
			human, waits = human || h, waits || w
		}
		return out, human, waits
	}

	contents := map[string][]string{} // normalized content → workflow IDs
	for _, w := range e.Workflows {
		if !isLibrary(w.Category) {
			contents[infos[w.ID].content] = append(contents[infos[w.ID].content], w.ID)
		}
	}

	for _, w := range e.Workflows {
		if isLibrary(w.Category) {
			t.Library++
			continue
		}
		in := infos[w.ID]
		reached, human, waits := wfSystems(w.ID, map[string]bool{})
		it := TriageItem{Kind: "workflow", ID: w.ID, Name: w.Name, Category: w.Category, Inputs: w.Inputs,
			UsedBy: orEmpty(uses["workflow:"+w.ID]), Runs: w.Runs, LastRun: w.LastRun, Systems: sortedKeys(reached)}
		var copyOf string
		for _, other := range contents[in.content] {
			if other != w.ID {
				copyOf = other
				break
			}
		}
		switch {
		case !inUse(it.UsedBy) && (copyOf != "" && w.Runs == 0 || stale(w.LastRun, now)):
			it.Group = GroupDrop
			it.Reasons = append(it.Reasons, unusedReason(it.UsedBy))
			if copyOf != "" {
				it.Reasons = append(it.Reasons, "Its content is the same as "+names[copyOf]+"'s.")
			}
			it.Reasons = append(it.Reasons, runReason(w.Runs, w.LastRun))
		case human || waits:
			it.Group = GroupTemporal
			if human {
				it.Reasons = append(it.Reasons, "It waits for people (a user interaction): a custom workflow asks with rvav's approvals (rvav.RequestApproval).")
			}
			if waits {
				it.Reasons = append(it.Reasons, "It waits for a timer or an event: a custom workflow waits as long as it needs, durably.")
			}
			if others := without(it.Systems, "vCenter"); len(others) > 0 {
				it.Reasons = append(it.Reasons, "It also reaches "+strings.Join(others, ", ")+": the custom workflow's worker does, with its own credentials.")
			}
		case len(it.Systems) == 0 || len(it.Systems) == 1 && it.Systems[0] == "vCenter":
			it.Group = GroupNative
			if len(it.Systems) == 0 {
				it.Reasons = append(it.Reasons, "It reaches no other system, as far as its content shows: check what its scripts do.")
			} else {
				it.Reasons = append(it.Reasons, "It only drives vCenter: rvav's built-in steps (or a composite item chaining them) do the same.")
			}
		default:
			it.Group = GroupAWX
			it.Reasons = append(it.Reasons, "It calls "+strings.Join(without(it.Systems, "vCenter"), ", ")+": a playbook does, with "+ansibleFor(it.Systems)+".")
		}
		if it.Group != GroupDrop {
			it.Reasons = append(it.Reasons, useReasons(it.UsedBy)...)
			if in.decision > 3 && it.Group != GroupTemporal {
				it.Reasons = append(it.Reasons, fmt.Sprintf("It branches %d times: if it compensates or runs for long, it may be C.", in.decision))
			}
			if r := runReason(w.Runs, w.LastRun); w.Runs == 0 || stale(w.LastRun, now) {
				it.Reasons = append(it.Reasons, r)
			}
		}
		it.Becomes = becomes[it.Group]
		t.Items = append(t.Items, it)
	}

	for _, a := range e.Actions {
		if isLibrary(a.Module) {
			t.Library++
			continue
		}
		used := orEmpty(uses["action:"+a.FQN()])
		it := TriageItem{Kind: "action", ID: a.ID, Name: a.FQN(), Category: a.Module, Inputs: a.Inputs, UsedBy: used,
			Systems: sortedKeys(actionSystems(a.FQN()))}
		var forms, workflows []string
		for _, u := range used {
			switch u.Kind {
			case "form_field":
				forms = append(forms, u.Name)
			case "workflow", "action":
				workflows = append(workflows, u.Name)
			}
		}
		switch {
		case len(forms) > 0:
			it.Group = GroupNative
			src := "a CEL expression"
			if slices.Contains(it.Systems, "REST") {
				src = "a REST value source"
			}
			it.Becomes = "A value source on the field"
			it.Reasons = append(it.Reasons, "It gives the choices of "+strings.Join(forms, ", ")+": in rvav, "+src+" on the field (ADR-0007).")
			if len(workflows) > 0 {
				it.Reasons = append(it.Reasons, "Workflows call it too: "+strings.Join(workflows, ", ")+".")
			}
		case len(workflows) > 0:
			it.Group = actionGroup(t.Items, workflows)
			it.Becomes = "Part of what replaces " + strings.Join(workflows, ", ")
			it.Reasons = append(it.Reasons, "Only workflows and actions call it ("+strings.Join(workflows, ", ")+"): it goes with them.")
		default:
			it.Group = GroupDrop
			it.Becomes = becomes[GroupDrop]
			it.Reasons = append(it.Reasons, "No form, workflow or action in vRA uses it.")
		}
		t.Items = append(t.Items, it)
	}

	// ABX actions: vRA runs them, the importer doesn't read their code.
	var abx []string
	for k := range uses {
		if id, ok := strings.CutPrefix(k, "abx:"); ok {
			abx = append(abx, id)
		}
	}
	sort.Strings(abx)
	for _, id := range abx {
		used := uses["abx:"+id]
		it := TriageItem{Kind: "abx_action", ID: id, Name: id, UsedBy: used, Systems: []string{}, Group: GroupAWX, Becomes: becomes[GroupAWX],
			Reasons: []string{"An ABX action (Python, Node.js or PowerShell): the importer doesn't read its code. Its script becomes a playbook, or the service it calls a webhook subscription."}}
		if !inUse(used) {
			it.Group, it.Becomes = GroupDrop, becomes[GroupDrop]
			it.Reasons = []string{unusedReason(used)}
		} else {
			it.Reasons = append(it.Reasons, useReasons(used)...)
		}
		t.Items = append(t.Items, it)
	}

	for _, it := range t.Items {
		t.Groups[it.Group]++
	}
	return t
}

// reach adds the systems the types and the script reach.
func reach(out map[string]bool, types []string, script string) {
	for _, s := range systems {
		for _, typ := range types {
			typ = strings.TrimPrefix(typ, "Array/")
			for _, pre := range s.Types {
				if strings.HasPrefix(typ, pre) {
					out[s.Name] = true
				}
			}
		}
		if script != "" && s.Script.MatchString(script) {
			out[s.Name] = true
		}
	}
}

func ansibleFor(reached []string) string {
	var out []string
	for _, s := range systems {
		if s.Ansible != "" && slices.Contains(reached, s.Name) && !slices.Contains(out, s.Ansible) {
			out = append(out, s.Ansible)
		}
	}
	if slices.Contains(reached, "vCenter") {
		out = append(out, "vmware.vmware (or rvav's built-in steps for the VM)")
	}
	if len(out) == 0 {
		return "the modules for them"
	}
	return strings.Join(out, ", ")
}

// rvavEvent is the rvav topic of a vRA event topic.
func rvavEvent(topic string) string {
	switch topic {
	case "compute.allocation.pre", "compute.provision.pre", "network.configure", "deployment.request.pre":
		return "provisioning.pre"
	case "compute.provision.post", "deployment.request.post":
		return "provisioning.post"
	case "deployment.resource.action.pre", "deployment.action.pre", "compute.reconfigure.pre":
		return "day2.pre"
	case "deployment.resource.action.post", "deployment.action.post", "compute.reconfigure.post":
		return "day2.post"
	case "compute.removal.pre":
		return "delete.pre"
	case "compute.removal.post":
		return "delete.post"
	}
	return ""
}

func useReasons(used []Use) []string {
	var out []string
	for _, u := range used {
		switch u.Kind {
		case "catalog_item":
			out = append(out, "The catalog item "+u.Name+" runs it: an rvav catalog item of the same name runs what replaces it.")
		case "subscription":
			if u.Disabled {
				continue
			}
			if strings.Contains(u.Detail, " in rvav") {
				out = append(out, "The subscription "+u.Name+" runs it ("+u.Detail+"): an rvav subscription runs what replaces it. rvav's events are per request, vRA's compute events per machine.")
			} else {
				out = append(out, "The subscription "+u.Name+" runs it ("+u.Detail+"): rvav has no such event, so this needs another trigger.")
			}
		case "day2_action":
			out = append(out, "The day-2 action "+u.Name+" runs it ("+u.Detail+"): an rvav custom day-2 action runs what replaces it.")
		}
	}
	return out
}

func unusedReason(used []Use) string {
	if len(used) > 0 {
		return "Only disabled subscriptions or actions use it."
	}
	return "No catalog item, subscription, day-2 action or workflow in vRA uses it."
}

func runReason(runs int, last *time.Time) string {
	if runs == 0 || last == nil {
		return "It has never run."
	}
	return fmt.Sprintf("It last ran on %s (%d runs kept).", last.Format("2 Jan 2006"), runs)
}

func stale(last *time.Time, now time.Time) bool { return last == nil || now.Sub(*last) > staleAfter }

func inUse(used []Use) bool {
	return slices.ContainsFunc(used, func(u Use) bool { return !u.Disabled })
}

// actionGroup: an action workflows call goes with them, in the most
// demanding of their groups.
func actionGroup(items []TriageItem, workflows []string) string {
	best := ""
	rank := map[string]int{GroupDrop: 0, GroupNative: 1, GroupAWX: 2, GroupTemporal: 3}
	for _, it := range items {
		if it.Kind == "workflow" && slices.Contains(workflows, it.Name) && (best == "" || rank[it.Group] > rank[best]) {
			best = it.Group
		}
	}
	if best == "" {
		return GroupAWX
	}
	return best
}

// normalContent is a workflow's content without its own ID and name, to
// find copies.
func normalContent(content, id, name string) string {
	return strings.NewReplacer(`"`+id+`"`, "", `"`+name+`"`, "").Replace(content)
}

func projectsOf(e *vra.Estate, ids []string) string {
	var out []string
	for _, id := range ids {
		out = append(out, projectName(e, id))
	}
	if len(out) == 0 {
		return ""
	}
	return "shared with " + strings.Join(out, ", ")
}

func sortedKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func without(list []string, x string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == x })
}

func orEmpty(u []Use) []Use {
	if u == nil {
		return []Use{}
	}
	return u
}

var groupTitles = map[string]string{
	GroupNative:   "A. Native in rvav",
	GroupAWX:      "B. AWX",
	GroupTemporal: "C. Custom Temporal workflow",
	GroupDrop:     "D. Retire",
}

// Markdown is the report for people: a table per group.
func (t Triage) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# vRO triage\n\nRead %s. Each workflow and action with a suggested group (ADR-0007) and why: people decide.\n",
		t.ReadAt.Format("2 Jan 2006 15:04 MST"))
	fmt.Fprintf(&b, "vRO's own library (%d workflows and actions) is left out.\n\n", t.Library)
	b.WriteString("| Group | Items |\n|---|---|\n")
	for _, g := range []string{GroupNative, GroupAWX, GroupTemporal, GroupDrop} {
		fmt.Fprintf(&b, "| %s | %d |\n", groupTitles[g], t.Groups[g])
	}
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") }
	for _, g := range []string{GroupNative, GroupAWX, GroupTemporal, GroupDrop} {
		if t.Groups[g] == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n| Name | Kind | Inputs | Used by | Last run | Reaches | Becomes | Why |\n|---|---|---|---|---|---|---|---|\n", groupTitles[g])
		for _, it := range t.Items {
			if it.Group != g {
				continue
			}
			var inputs, used []string
			for _, p := range it.Inputs {
				inputs = append(inputs, p.Name+" ("+p.Type+")")
			}
			for _, u := range it.UsedBy {
				s := strings.ReplaceAll(u.Kind, "_", " ") + " " + u.Name
				if u.Detail != "" {
					s += " (" + u.Detail + ")"
				}
				if u.Disabled {
					s += ", disabled"
				}
				used = append(used, s)
			}
			last := "never"
			if it.LastRun != nil {
				last = it.LastRun.Format("2006-01-02")
			}
			if it.Kind != "workflow" {
				last = "–" // vRO keeps runs of workflows only
			}
			name := it.Name
			if it.Category != "" && it.Kind == "workflow" {
				name += " (" + it.Category + ")"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", cell(name), strings.ReplaceAll(it.Kind, "_", " "),
				cell(strings.Join(inputs, ", ")), cell(strings.Join(used, "; ")), last, cell(strings.Join(it.Systems, ", ")),
				cell(it.Becomes), cell(strings.Join(it.Reasons, " ")))
		}
	}
	return b.String()
}
