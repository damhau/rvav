package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/naming"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

var paramRef = regexp.MustCompile(`\$\{inputs\.([a-zA-Z0-9_]+)\}`)

// resolveParams substitutes ${inputs.x} references with current form values.
func resolveParams(params *map[string]interface{}, inputs map[string]any) map[string]string {
	out := map[string]string{}
	if params == nil {
		return out
	}
	for k, v := range *params {
		s, ok := v.(string)
		if !ok {
			out[k] = fmt.Sprint(v)
			continue
		}
		out[k] = paramRef.ReplaceAllStringFunc(s, func(m string) string {
			name := paramRef.FindStringSubmatch(m)[1]
			if iv, ok := inputs[name].(string); ok {
				return iv
			}
			return ""
		})
	}
	return out
}

func option(value, label, meta string) api.Option {
	o := api.Option{Value: value, Label: label}
	if meta != "" {
		o.Meta = &meta
	}
	return o
}

// vmFolders are the VM folders of the datacenters whose clusters the
// project's placement allows in that environment, once per path: a
// component's folder is a path, found in the datacenter it is placed in.
func (s *Service) vmFolders(ctx context.Context, projectID, env string) ([]store.InvFolder, error) {
	pl, err := s.placement(ctx, projectID, env)
	if err != nil {
		return nil, err
	}
	datacenters := map[[2]string]bool{}
	for _, c := range pl.clusters {
		datacenters[[2]string{c.VCenter, c.Datacenter}] = true
	}
	all, err := s.Store.InventoryFolders(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.InvFolder
	for _, f := range all {
		if datacenters[[2]string{f.VCenter, f.Datacenter}] && !slices.ContainsFunc(out, func(o store.InvFolder) bool { return o.Path == f.Path }) {
			out = append(out, f)
		}
	}
	return out, nil
}

// folderOptions are VM folders by path, each under its parent folder when
// that one is offered too. A tree names a folder under its parent by its
// own name; a list, and a tree at its top, by its path.
func folderOptions(folders []store.InvFolder, tree bool) []api.Option {
	offered := map[string]bool{}
	for _, f := range folders {
		offered[f.Path] = true
	}
	opts := []api.Option{}
	for _, f := range folders {
		o := option(f.Path, f.Path, f.Datacenter)
		if i := strings.LastIndex(f.Path, "/"); i > 0 && offered[f.Path[:i]] {
			o.Parent = present.Ptr(f.Path[:i])
			if tree {
				o.Label = folderName.Replace(f.Path[i+1:])
			}
		}
		opts = append(opts, o)
	}
	return opts
}

// folderName undoes how vSphere escapes a name in a path.
var folderName = strings.NewReplacer("%2f", "/", "%2F", "/", "%5c", `\`, "%5C", `\`, "%25", "%")

func formatGB(gb int64) string {
	if gb >= 1024 {
		return fmt.Sprintf("%.1f TB free", float64(gb)/1024)
	}
	return fmt.Sprintf("%d GB free", gb)
}

// FieldValues resolves a form field's options or computed value.
func (s *Service) FieldValues(ctx context.Context, p *auth.Principal, q api.FieldValuesQuery) (api.FieldValues, error) {
	out := api.FieldValues{Field: q.Field}
	// The form designer resolves any version, as the user it previews as.
	designer := q.AsUserId != nil
	if designer {
		as, err := s.previewPrincipal(ctx, p, q.AsUserId, q.ProjectId)
		if err != nil {
			return out, err
		}
		p = as
	}
	proj, err := s.visibleProject(ctx, p, q.ProjectId)
	if err != nil {
		return out, err
	}
	ver, err := s.Store.GetVersion(ctx, q.VersionId)
	if errors.Is(err, store.ErrNotFound) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !designer {
		if ok, err := s.SharedWithRequester(ctx, p, ver.ItemID, proj.ID); err != nil {
			return out, err
		} else if !ok {
			return out, ErrNotFound
		}
		if ver.State != "published" && !p.Everywhere(auth.CatalogManage) {
			return out, ErrNotFound // drafts and earlier versions are for catalog admins
		}
	}
	var ui api.UiSchema
	if err := json.Unmarshal(ver.UISchema, &ui); err != nil {
		return out, err
	}
	field, ok := ui.Fields[q.Field]
	if !ok {
		return out, &BadRequestError{Msg: fmt.Sprintf("The form has no field %q.", q.Field)}
	}
	env := ""
	if q.Environment != nil {
		env = *q.Environment
	}
	inputs := maps.Clone(q.Inputs)
	if inputs == nil {
		inputs = map[string]any{}
	}
	withStart(ui, startOf(ver.FormSchema, ver.InitialValues), inputs)
	return s.sourceValues(ctx, p, proj, env, ver, q.Field, field, inputs, deref(q.Search))
}

// sourceValues resolves a field's value source for a principal requesting
// in a project and environment. Given searched text, it keeps the options
// whose value, label or detail contains it, unless the source finds by the
// text itself.
func (s *Service) sourceValues(ctx context.Context, p *auth.Principal, proj store.Project, env string, ver store.ItemVersion,
	name string, field api.UiField, inputs map[string]any, search string) (api.FieldValues, error) {
	out, itself, err := s.resolveSource(ctx, p, proj, env, ver, name, field, inputs, search)
	if defaultRule(field) != "" {
		// The field's default rule wins over the option the source suggests.
		out.Default = nil
	}
	if err != nil || strings.TrimSpace(search) == "" || out.Options == nil || itself {
		return out, err
	}
	kept := matching(*out.Options, search)
	out.Options = &kept
	return out, nil
}

// searchesItself says whether a source finds options by the searched text
// rather than rvav keeping those that match it.
func searchesItself(src *api.ValueSource) bool {
	return src != nil && (src.Search != nil && *src.Search || src.Kind == api.Builtin && deref(src.Name) == "rvav.users")
}

// matching are the options whose value, label or detail contains text,
// ignoring case.
func matching(opts []api.Option, text string) []api.Option {
	want := strings.ToLower(strings.TrimSpace(text))
	out := []api.Option{}
	for _, o := range opts {
		if slices.ContainsFunc([]string{o.Value, o.Label, deref(o.Meta)}, func(s string) bool {
			return strings.Contains(strings.ToLower(s), want)
		}) {
			out = append(out, o)
		}
	}
	return out
}

// resolveSource resolves a field's value source, and says whether the
// source found its options by the searched text itself. A library source
// is its entry's, the entry's parameters given by the field.
func (s *Service) resolveSource(ctx context.Context, p *auth.Principal, proj store.Project, env string, ver store.ItemVersion,
	name string, field api.UiField, inputs map[string]any, search string) (api.FieldValues, bool, error) {
	out := api.FieldValues{Field: name}
	src := sourceOf(field)
	if src == nil {
		if field.Options != nil {
			out.Options = field.Options
		}
		return out, false, nil
	}
	refs := inputRefs(inputs)
	eval := policy.EvalSource
	vars := func() policy.Vars {
		v := formVars(inputs, proj, env, p, ver)
		v["search"] = search
		return v
	}
	if src.Kind == api.Library {
		e, params, err := s.libraryBound(ctx, deref(src.Name), derefInt(src.Version), api.Source, derefMap(src.Params), vars(), formProps(ver.FormSchema))
		if errors.Is(err, errUnbound) {
			if !singleSource(field) {
				out.Options = &[]api.Option{}
			}
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		own := vars
		vars = func() policy.Vars { return libraryVars(params, own()) }
		refs, eval = Refs{"params": params}, policy.EvalLibrary
		entryParams := map[string]any{}
		_ = json.Unmarshal(e.Params, &entryParams)
		src = &api.ValueSource{Kind: api.ValueSourceKind(e.Kind), Params: &entryParams, Search: &e.Search}
	}
	raw := map[string]any{}
	if src.Params != nil {
		raw = *src.Params
	}
	itself := searchesItself(src)
	switch src.Kind {
	case api.Rest:
		secret := func(name string) (store.Secret, string, error) { return s.secretValue(ctx, proj.ID, name) }
		if singleSource(field) {
			value, cacheSeconds, err := s.REST.Value(ctx, raw, refs, secret)
			if err != nil {
				return out, itself, err
			}
			out.Value, out.CacheSeconds = value, &cacheSeconds
			return out, itself, nil
		}
		opts, cacheSeconds, err := s.REST.Options(ctx, raw, refs, search, secret)
		if err != nil {
			return out, itself, err
		}
		out.Options, out.CacheSeconds = &opts, &cacheSeconds
		return out, itself, nil
	case api.Cel:
		opts, value, err := celValues(raw, vars(), eval)
		if err != nil {
			return out, itself, err
		}
		out.Options, out.Value = opts, value
		return out, itself, nil
	}
	out, err := s.builtinValues(ctx, p, proj, env, ver, name, field, *src, inputs, search)
	return out, itself, err
}

// builtinValues resolves a field's built-in value source.
func (s *Service) builtinValues(ctx context.Context, p *auth.Principal, proj store.Project, env string, ver store.ItemVersion,
	name string, field api.UiField, src api.ValueSource, inputs map[string]any, search string) (api.FieldValues, error) {
	out := api.FieldValues{Field: name}
	if src.Kind != api.Builtin || src.Name == nil {
		return out, &BadRequestError{Msg: fmt.Sprintf("Value source %q of %s is not supported.", src.Kind, name)}
	}
	params := resolveParams(src.Params, inputs)
	needEnv := func() error {
		if env == "" {
			return &BadRequestError{Msg: "Choose an environment first: " + *src.Name + " depends on it."}
		}
		return nil
	}
	cache := func(sec int) { out.CacheSeconds = present.Ptr(sec) }

	switch *src.Name {
	case "vsphere.clusters":
		if err := needEnv(); err != nil {
			return out, err
		}
		pl, err := s.placement(ctx, proj.ID, env)
		if err != nil {
			return out, err
		}
		opts := []api.Option{}
		best, bestFree := "", -1.0
		for _, c := range pl.clusters {
			free := 0.0
			if c.CPUTotalMHz > 0 {
				free = 100 * float64(c.CPUTotalMHz-c.CPUUsedMHz) / float64(c.CPUTotalMHz)
			}
			opts = append(opts, option(c.Name, c.Name, fmt.Sprintf("%d hosts · %.0f%% CPU free", c.HostCount, free)))
			if free > bestFree {
				best, bestFree = c.Name, free
			}
		}
		out.Options = &opts
		if best != "" {
			out.Default = &best
		}
		cache(30)

	case "vsphere.datastores", "vsphere.networks":
		if err := needEnv(); err != nil {
			return out, err
		}
		cluster := params["cluster"]
		opts := []api.Option{}
		out.Options = &opts
		if cluster == "" {
			return out, nil
		}
		pl, err := s.placement(ctx, proj.ID, env)
		if err != nil {
			return out, err
		}
		if *src.Name == "vsphere.datastores" {
			list := pl.datastoresFor(cluster)
			sort.SliceStable(list, func(i, j int) bool { return list[i].FreeGB > list[j].FreeGB })
			for _, d := range list {
				opts = append(opts, option(d.Name, d.Name, formatGB(d.FreeGB)))
			}
		} else {
			for _, n := range pl.networksFor(cluster) {
				meta := "no subnet linked"
				o := option(n.Name, n.Name, "")
				if sn, ok := pl.subnets[n.Name]; ok {
					meta = sn.Subnet
				} else {
					o.Disabled = present.Ptr(true)
					o.DisabledReason = present.Ptr("No EfficientIP subnet is linked to this network.")
				}
				o.Meta = &meta
				opts = append(opts, o)
			}
		}
		out.Options = &opts
		for _, o := range opts {
			if o.Disabled == nil {
				v := o.Value
				out.Default = &v
				break
			}
		}
		cache(30)

	case "vsphere.templates":
		list, err := s.Store.InventoryTemplates(ctx)
		if err != nil {
			return out, err
		}
		opts := []api.Option{}
		for _, t := range list {
			opts = append(opts, option(t.Name, t.Name, t.GuestOS))
		}
		out.Options = &opts
		cache(60)

	case "vsphere.folders":
		if err := needEnv(); err != nil {
			return out, err
		}
		folders, err := s.vmFolders(ctx, proj.ID, env)
		if err != nil {
			return out, err
		}
		out.Options = present.Ptr(folderOptions(folders, field.Widget == api.UiWidgetTree))
		cache(60)

	case "rvav.templates":
		if err := needEnv(); err != nil {
			return out, err
		}
		list, err := s.offeredTemplates(ctx, proj.ID, env)
		if err != nil {
			return out, err
		}
		inv, err := s.Store.InventoryTemplates(ctx)
		if err != nil {
			return out, err
		}
		added, err := s.addedVCenters(ctx)
		if err != nil {
			return out, err
		}
		opts := []api.Option{}
		for _, t := range list {
			var names []string
			found := false
			for _, src := range t.Sources {
				if !slices.Contains(names, src.Template) {
					names = append(names, src.Template)
				}
				found = found || inInventory(src, inv, added)
			}
			o := option(t.Key, t.Label, strings.Join(names, ", "))
			if t.Description != "" {
				o.Description = &t.Description
			}
			if !found {
				what := "it"
				if len(t.Sources) == 1 {
					what = sourceText(t.Sources[0])
				}
				o.Disabled, o.DisabledReason = present.Ptr(true), present.Ptr("vCenter doesn't have "+what+" now.")
			}
			opts = append(opts, o)
		}
		out.Options = &opts
		cache(60)

	case "rvav.sizes":
		if err := needEnv(); err != nil {
			return out, err
		}
		list, err := s.offeredSizes(ctx, proj.ID, env)
		if err != nil {
			return out, err
		}
		opts := []api.Option{}
		for _, z := range list {
			opts = append(opts, option(z.Key, z.Label, sizeMeta(z)))
		}
		out.Options = &opts
		cache(60)

	case "ipam.next_free_address":
		network := params["network"]
		if network == "" {
			return out, nil
		}
		sn, err := s.Store.NetworkSubnet(ctx, network)
		if errors.Is(err, store.ErrNotFound) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		ip, err := s.IPAM.NextFree(ctx, subnetRef(sn))
		if errors.Is(err, efficientip.ErrSubnetFull) {
			return out, nil
		}
		if err != nil {
			return out, &UpstreamError{System: "EfficientIP", Err: err}
		}
		out.Value = ip
		cache(10)

	case "naming.preview":
		if err := needEnv(); err != nil {
			return out, err
		}
		bp, err := blueprint.Parse(ver.Blueprint)
		if err != nil {
			return out, err
		}
		spec, err := s.namingFor(ctx, proj.ID, env, bp.Naming)
		if err != nil {
			return out, err
		}
		prefix, err := spec.Template.Prefix(naming.Values(proj.Code, env, inputs))
		if err != nil {
			return out, nil // the inputs the name needs are not filled in yet
		}
		zone := ""
		if network, _ := inputs["network"].(string); network != "" {
			if sn, err := s.Store.NetworkSubnet(ctx, network); err == nil {
				zone = sn.DNSZone
			}
		}
		next, _, names, err := s.peekNames(ctx, spec, prefix, zone, 1, nil)
		if err != nil {
			return out, err
		}
		if names.err != nil {
			s.Log.Warn("checking VM names in DNS", "err", names.err)
		}
		if len(next) > 0 {
			out.Value = next[0]
		}
		cache(5)

	case "ipam.subnets":
		subnets, err := s.Store.NetworkSubnets(ctx)
		if err != nil {
			return out, err
		}
		seen := map[string]bool{}
		opts := []api.Option{}
		for _, sn := range subnets {
			if network := params["network"]; (network != "" && sn.Network != network) || seen[sn.Subnet] {
				continue
			}
			seen[sn.Subnet] = true
			label := sn.Subnet
			if sn.SubnetName != "" {
				label += " · " + sn.SubnetName
			}
			opts = append(opts, option(sn.Subnet, label, "Gateway "+sn.Gateway))
		}
		sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
		out.Options = &opts
		cache(60)

	case "awx.inventories", "awx.credentials":
		opts, err := s.awxOptions(ctx, *src.Name, params)
		if err != nil {
			return out, err
		}
		out.Options = &opts
		cache(60)

	case "nsx.segments", "nsx.tier1s", "nsx.groups", "nsx.services":
		opts, err := s.nsxOptions(ctx, proj.ID, env, *src.Name, params)
		if err != nil {
			return out, err
		}
		out.Options = &opts
		cache(60)

	case "rvav.projects", "rvav.users", "rvav.deployments", "rvav.vms":
		opts, err := s.rvavOptions(ctx, p, proj, *src.Name, params, search)
		if err != nil {
			return out, err
		}
		out.Options = &opts
		cache(30)

	case "dns.zones":
		zones, err := s.dnsZones(ctx)
		if err != nil {
			return out, err
		}
		opts := []api.Option{}
		for _, z := range zones {
			opts = append(opts, option(z, z, ""))
		}
		out.Options = &opts
		cache(300)

	default:
		if strings.HasPrefix(*src.Name, "awx.") {
			return out, &BadRequestError{Msg: fmt.Sprintf("Value source %s is not available yet.", *src.Name)}
		}
		return out, &BadRequestError{Msg: fmt.Sprintf("Unknown value source %s.", *src.Name)}
	}
	return out, nil
}

// dnsZones lists the forward zones of rvav's DNS server, sorted (those of
// the network mappings without EfficientIP).
func (s *Service) dnsZones(ctx context.Context) ([]string, error) {
	var zones []string
	if s.IPAM != nil {
		list, err := s.IPAM.Zones(ctx)
		if err != nil {
			return nil, &UpstreamError{System: "EfficientIP", Err: err}
		}
		zones = list
	} else {
		subnets, err := s.Store.NetworkSubnets(ctx)
		if err != nil {
			return nil, err
		}
		for _, sn := range subnets {
			zones = append(zones, sn.DNSZone)
		}
	}
	slices.Sort(zones)
	return slices.Compact(zones), nil
}

// awxOptions are the inventories, or the credentials (of params["kind"]
// when set), of rvav's AWX organisation.
func (s *Service) awxOptions(ctx context.Context, source string, params map[string]string) ([]api.Option, error) {
	if !awx.IsSet(s.AWX) {
		return nil, &UpstreamError{System: "AWX", Err: errors.New("no AWX is configured")}
	}
	opts := []api.Option{}
	if source == "awx.inventories" {
		list, err := s.AWX.Inventories(ctx, s.AWXOrganization)
		if err != nil {
			return nil, &UpstreamError{System: "AWX", Err: err}
		}
		for _, i := range list {
			meta := ""
			if i.Kind == "smart" {
				meta = "Smart inventory"
			}
			opts = append(opts, option(strconv.Itoa(i.ID), i.Name, meta))
		}
		return opts, nil
	}
	list, err := s.AWX.Credentials(ctx, s.AWXOrganization, params["kind"])
	if err != nil {
		return nil, &UpstreamError{System: "AWX", Err: err}
	}
	for _, c := range list {
		opts = append(opts, option(strconv.Itoa(c.ID), c.Name, c.Type))
	}
	return opts, nil
}

// awxChoices are the values awxOptions offers.
func (s *Service) awxChoices(ctx context.Context, source string, params map[string]string) ([]string, error) {
	opts, err := s.awxOptions(ctx, source, params)
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Value
	}
	return out, err
}

// nsxOptions are what NSX offers a form (ADR-0028): existing segments, by
// the port group's name a network binding takes (on a Tier-1, with tier1);
// Tier-1 gateways; groups VMs can join by tag, as nsx:<ID> a groups binding
// takes; services. What rvav made for deployments isn't offered. They are
// those of the NSX Manager serving the vCenter of the cluster or vcenter
// param, else of the vCenters the placement allows (ADR-0062).
func (s *Service) nsxOptions(ctx context.Context, projectID, env, name string, params map[string]string) ([]api.Option, error) {
	managers, err := s.offeredNSXManagers(ctx, projectID, env, name, params)
	if err != nil {
		return nil, err
	}
	opts := []api.Option{}
	seen := map[string]bool{}
	for _, m := range managers {
		client, err := s.nsxClient(ctx, m)
		if err != nil {
			return nil, err
		}
		got, err := nsxOptionsOf(ctx, client, name, params)
		if err != nil {
			return nil, err
		}
		for _, o := range got {
			if !seen[o.Value] {
				seen[o.Value] = true
				opts = append(opts, o)
			}
		}
	}
	sort.Slice(opts, func(i, j int) bool { return opts[i].Label < opts[j].Label })
	return opts, nil
}

// offeredNSXManagers are the NSX Managers a form's NSX choices come from.
func (s *Service) offeredNSXManagers(ctx context.Context, projectID, env, name string, params map[string]string) ([]string, error) {
	all, err := s.allNSXManagers(ctx)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, &BadRequestError{Msg: "rvav has no NSX Manager: " + name + " needs one, set in Integrations."}
	}
	vcenter := params["vcenter"]
	var pl *placement
	if env != "" && projectID != "" && (vcenter == "" || params["cluster"] != "") {
		if pl, err = s.placement(ctx, projectID, env); err != nil {
			return nil, err
		}
	}
	if c := params["cluster"]; vcenter == "" && c != "" && pl != nil {
		if cl := pl.cluster(c); cl != nil {
			vcenter = cl.VCenter
		}
	}
	if vcenter != "" {
		m, ok, err := s.nsxManagerOf(ctx, vcenter)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &BadRequestError{Msg: "No NSX Manager serves vCenter " + vcenter + "."}
		}
		return []string{m}, nil
	}
	if pl == nil {
		return all, nil
	}
	var out []string
	for _, c := range pl.clusters {
		m, ok, err := s.nsxManagerOf(ctx, c.VCenter)
		if err != nil {
			return nil, err
		}
		if ok && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out, nil
}

// nsxOptionsOf are what one NSX Manager offers.
func nsxOptionsOf(ctx context.Context, client *nsx.Client, name string, params map[string]string) ([]api.Option, error) {
	ours := func(tags []nsx.Tag) bool {
		return slices.ContainsFunc(tags, func(t nsx.Tag) bool { return t.Scope == nsx.OwnerScope })
	}
	opts := []api.Option{}
	switch name {
	case "nsx.segments":
		segs, err := client.Segments(ctx)
		if err != nil {
			return nil, &UpstreamError{System: "NSX", Err: err}
		}
		for _, sg := range segs {
			t1 := strings.TrimPrefix(sg.ConnectivityPath, nsx.Tier1Path)
			if ours(sg.Tags) || params["tier1"] != "" && t1 != params["tier1"] {
				continue
			}
			meta := cmp.Or(t1, "No Tier-1")
			if len(sg.Subnets) > 0 {
				meta += " · " + sg.Subnets[0].GatewayAddress
			}
			opts = append(opts, option(sg.DisplayName, sg.DisplayName, meta))
		}
	case "nsx.tier1s":
		t1s, err := client.Tier1s(ctx)
		if err != nil {
			return nil, &UpstreamError{System: "NSX", Err: err}
		}
		for _, t := range t1s {
			opts = append(opts, option(t.ID, cmp.Or(t.DisplayName, t.ID), ""))
		}
	case "nsx.groups":
		groups, err := client.Groups(ctx)
		if err != nil {
			return nil, &UpstreamError{System: "NSX", Err: err}
		}
		for _, g := range groups {
			tags, ok := g.JoinTags()
			if !ok || g.RvavMade() {
				continue
			}
			var by []string
			for _, t := range tags {
				by = append(by, t.Scope+"|"+t.Tag)
			}
			opts = append(opts, option("nsx:"+g.ID, cmp.Or(g.DisplayName, g.ID), "Tag "+strings.Join(by, ", ")))
		}
	case "nsx.services":
		svcs, err := client.Services(ctx)
		if err != nil {
			return nil, &UpstreamError{System: "NSX", Err: err}
		}
		for _, sv := range svcs {
			opts = append(opts, option(sv.ID, cmp.Or(sv.DisplayName, sv.ID), ""))
		}
	}
	return opts, nil
}
