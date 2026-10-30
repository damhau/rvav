package vraimport

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// vRA's NSX resources (ADR-0028): an on-demand network (routed, outbound or
// private) becomes an NSX segment made for each deployment; a new security
// group, a group made for each deployment with the inbound rules rvav can
// write; an existing security group can't be told from its constraint tags,
// and is reported.

// onDemand are the network types vRA makes a network of for each
// deployment.
var onDemand = []string{"routed", "outbound", "private"}

// nsxKey is a resource name as a segment's or group's key: lower-case
// letters and digits, starting with a letter, at most 16, not taken.
func nsxKey(name string, taken map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' && b.Len() > 0 {
			b.WriteRune(r)
		}
	}
	k := b.String()
	if k == "" {
		k = "net"
	}
	k = k[:min(len(k), 14)]
	key := k
	for i := 2; taken[key]; i++ {
		key = fmt.Sprintf("%s%d", k, i)
	}
	taken[key] = true
	return key
}

// portServices are NSX's services for the usual protocols and ports.
var portServices = map[string]string{"tcp/22": "SSH", "tcp/80": "HTTP", "tcp/443": "HTTPS", "tcp/3389": "RDP",
	"tcp/3306": "MySQL", "tcp/5432": "PostgreSQL", "tcp/1433": "MS-SQL-S"}

// nsxPlan is what a template's NSX resources become.
type nsxPlan struct {
	segments map[string]string // network resource → segment key
	groups   map[string]string // new security group resource → group key
	existing map[string]bool   // existing security group resources
	security []map[string]any  // the blueprint's security_groups
	used     map[string]bool   // segments the machines are on
}

// mapNSX maps a template's networks made per deployment and its security
// groups.
func mapNSX(t tmpl, note func(level, where, text string)) *nsxPlan {
	p := &nsxPlan{segments: map[string]string{}, groups: map[string]string{}, existing: map[string]bool{}, used: map[string]bool{}}
	taken := map[string]bool{}
	names := slices.Sorted(maps.Keys(t.Resources))
	for _, name := range names {
		r := t.Resources[name]
		if !slices.Contains(networkTypes, r.Type) {
			continue
		}
		if nt, _ := r.Properties["networkType"].(string); slices.Contains(onDemand, nt) {
			p.segments[name] = nsxKey(name, taken)
			text := fmt.Sprintf("vRA's %s network is an NSX segment made for each deployment (%s): a placement rule of the project and environment says where (its Tier-1 and EfficientIP block).",
				nt, p.segments[name])
			if nt == "private" {
				text += " vRA's was isolated: rvav's segments connect to the rule's Tier-1."
			}
			note(Info, "resources."+name, text)
		}
	}
	for _, name := range names {
		r := t.Resources[name]
		if r.Type != "Cloud.SecurityGroup" {
			continue
		}
		if kind, _ := r.Properties["securityGroupType"].(string); kind == "existing" {
			p.existing[name] = true
			note(Unmapped, "resources."+name, "An existing security group, chosen by constraint tags: rvav can't tell which NSX group it is. Add it to the blueprint's groups as nsx:<group ID>.")
			continue
		}
		p.groups[name] = nsxKey(name, taken)
	}
	for _, name := range names {
		key, ok := p.groups[name]
		if !ok {
			continue
		}
		g := map[string]any{"key": key, "title": name}
		var rules []any
		list, _ := t.Resources[name].Properties["rules"].([]any)
		for i, x := range list {
			raw, _ := x.(map[string]any)
			rule, why := p.rule(raw)
			if why != "" {
				note(Unmapped, fmt.Sprintf("resources.%s.rules[%d]", name, i), "Left out: "+why+".")
				continue
			}
			rules = append(rules, rule)
		}
		if len(rules) > 0 {
			g["rules"] = rules
		}
		p.security = append(p.security, g)
		note(Info, "resources."+name, fmt.Sprintf("A security group made for each deployment (%s), with %d of its %d rules.", key, len(rules), len(list)))
	}
	return p
}

// rule is a vRA firewall rule as rvav's, into its group; why says what
// rvav can't write.
func (p *nsxPlan) rule(r map[string]any) (map[string]any, string) {
	str := func(k string) string { s, _ := r[k].(string); return strings.TrimSpace(s) }
	if d := strings.ToLower(str("direction")); d == "outbound" {
		return nil, "an outbound rule: rvav's rules let traffic into a group"
	}
	from := "any"
	switch src := str("source"); {
	case src == "" || strings.EqualFold(src, "any"):
	case p.groups[resourceRef(src)] != "":
		from = p.groups[resourceRef(src)]
	case p.existing[resourceRef(src)]:
		return nil, "from an existing security group rvav can't tell"
	default:
		return nil, "from addresses (" + src + "): rvav's rules are between groups"
	}
	var services []any
	if s := str("service"); s != "" {
		services = []any{s}
	} else {
		proto := strings.ToLower(cmp.Or(str("protocol"), "any"))
		var ports []string
		if v := r["ports"]; v != nil {
			ports = strings.FieldsFunc(fmt.Sprint(v), func(r rune) bool { return r == ',' || r == ' ' })
		}
		switch {
		case proto == "any" && len(ports) == 0:
			services = []any{"any"}
		case strings.HasPrefix(proto, "icmp"):
			services = []any{"ICMP-ALL"}
		default:
			for _, port := range ports {
				sv, ok := portServices[proto+"/"+port]
				if !ok {
					return nil, fmt.Sprintf("%s port %s: no NSX service of rvav's matches it; name one with service", strings.ToUpper(proto), port)
				}
				services = append(services, sv)
			}
			if len(services) == 0 {
				return nil, "no ports nor service"
			}
		}
	}
	out := map[string]any{"from": []any{from}, "services": services}
	switch strings.ToLower(str("access")) {
	case "deny":
		out["action"] = "reject"
	case "drop":
		out["action"] = "drop"
	}
	return out, ""
}

// machine maps a machine's networks to NSX: its first network's segment,
// if made for the deployment, and the groups of its networks.
func (p *nsxPlan) machine(props map[string]any, where string, c, bindings map[string]any, note func(level, where, text string)) {
	nets, _ := props["networks"].([]any)
	var groups, nics []any
	for i, x := range nets {
		n, _ := x.(map[string]any)
		ref := resourceRef(fmt.Sprint(n["network"]))
		key, onSegment := p.segments[ref]
		switch {
		case onSegment && i == 0:
			bindings["network"] = "segments." + key
			p.used[ref] = true
		case onSegment:
			// An added adapter on it: its address from the segment's subnet.
			nics = append(nics, map[string]any{"key": fmt.Sprintf("nic%d", i+1), "network": "segments." + key})
			p.used[ref] = true
		case i > 0:
			note(Warning, where+".networks", fmt.Sprintf("Adapter %d is on an existing network: add it to the blueprint's nics, with the network placement allows.", i+1))
		}
		sgs, _ := n["securityGroups"].([]any)
		for _, s := range sgs {
			ref := resourceRef(fmt.Sprint(s))
			if key, ok := p.groups[ref]; ok && !slices.Contains(groups, any(key)) {
				groups = append(groups, key)
			}
		}
	}
	if len(groups) > 0 {
		c["groups"] = groups
	}
	if len(nics) > 0 {
		c["nics"] = nics
	}
}

// apply adds the segments and security groups to the blueprint.
func (p *nsxPlan) apply(bp map[string]any) {
	var segs []any
	for _, name := range slices.Sorted(maps.Keys(p.used)) {
		segs = append(segs, map[string]any{"key": p.segments[name], "title": name})
	}
	if len(segs) > 0 {
		bp["segments"] = segs
	}
	if len(p.security) > 0 {
		sg := make([]any, len(p.security))
		for i, g := range p.security {
			sg[i] = g
		}
		bp["security_groups"] = sg
	}
}
