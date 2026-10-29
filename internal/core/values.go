package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/naming"
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
		if ok, err := s.Store.IsEntitled(ctx, ver.ItemID, proj.ID); err != nil {
			return out, err
		} else if !ok {
			return out, ErrNotFound
		}
		if ver.State != "published" && !canAdminCatalog(p) {
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
	if field.ValueSource == nil {
		if field.Options != nil {
			out.Options = field.Options
		}
		return out, nil
	}
	src := field.ValueSource
	env := ""
	if q.Environment != nil {
		env = *q.Environment
	}
	raw := map[string]any{}
	if src.Params != nil {
		raw = *src.Params
	}
	switch src.Kind {
	case api.Rest:
		opts, cacheSeconds, err := s.REST.Options(ctx, raw, q.Inputs, func(name string) (store.Secret, string, error) {
			return s.secretValue(ctx, name)
		})
		if err != nil {
			return out, err
		}
		out.Options, out.CacheSeconds = &opts, &cacheSeconds
		return out, nil
	case api.Cel:
		opts, value, err := celValues(raw, formVars(q.Inputs, proj, env, p))
		if err != nil {
			return out, err
		}
		out.Options, out.Value = opts, value
		return out, nil
	}
	if src.Kind != api.Builtin || src.Name == nil {
		return out, &BadRequestError{Msg: fmt.Sprintf("Value source %q of %s is not supported.", src.Kind, q.Field)}
	}
	params := resolveParams(src.Params, q.Inputs)
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

	case "rvav.templates":
		if err := needEnv(); err != nil {
			return out, err
		}
		list, err := s.offeredTemplates(ctx, proj.ID, env)
		if err != nil {
			return out, err
		}
		inventory, err := s.inventoryTemplates(ctx)
		if err != nil {
			return out, err
		}
		opts := []api.Option{}
		for _, t := range list {
			o := option(t.Key, t.Label, t.VCenterTemplate)
			if t.Description != "" {
				o.Description = &t.Description
			}
			if !inventory[t.ContentLibrary+"/"+t.VCenterTemplate] {
				what := t.VCenterTemplate
				if t.ContentLibrary != "" {
					what += " in content library " + t.ContentLibrary
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
		prefix, err := spec.Template.Prefix(naming.Values(proj.Code, env, q.Inputs))
		if err != nil {
			return out, nil // the inputs the name needs are not filled in yet
		}
		zone := ""
		if network, _ := q.Inputs["network"].(string); network != "" {
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
	if s.AWX == nil {
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
