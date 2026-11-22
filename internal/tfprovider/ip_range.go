package tfprovider

import (
	"context"
	"io"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func newIPRangeResource() resource.Resource {
	return collection{
		get: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.GetIpRange(ctx, id)
		},
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateIpRangeWithBody(ctx, jsonType, b)
		},
		update: func(ctx context.Context, c *conn, id string, b io.Reader) (*http.Response, error) {
			return c.api.UpdateIpRangeWithBody(ctx, id, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteIpRange(ctx, id)
		},
	}.resource("ip_range", schema.Schema{
		MarkdownDescription: "A range of addresses rvav gives VMs itself, on a network EfficientIP doesn't manage. " +
			"A network with a range takes its new VMs' addresses from its ranges instead of EfficientIP. rvav writes no DNS records for them. " +
			"While VMs hold addresses from a range, rvav refuses to destroy it, to change its vCenter, network, subnet or " +
			"gateway, and to leave out an address a VM holds.",
		Attributes: map[string]schema.Attribute{
			"id":          idAttribute("range"),
			"name":        required("The range's name."),
			"description": optional("What it is for."),
			"vcenter":     required("The vCenter the network is in, by name, for example `vc01.corp.example`."),
			"network":     required("The port group, or an NSX segment's port group, as vCenter names it."),
			"cidr": required("The network's IPv4 subnet, such as `10.51.0.0/24`, its host bits zero. Every range of " +
				"a network has the same subnet and gateway."),
			"start_address": required("The range's first address."),
			"end_address":   required("The range's last address. Ranges of a vCenter don't overlap."),
			"gateway":       required("The network's gateway: never given."),
			"dns_servers":   stringList("The DNS servers VMs on the network use."),
			"dns_search_domains": stringList("The domains VMs search, after their own. rvav writes no DNS records " +
				"in them."),
			"dns_domain": optional("The domain VMs on the network are named in. Default: the blueprint's guest domain. " +
				"rvav writes no DNS records in it."),
			"excluded": schema.SetAttribute{Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Addresses the range never gives, each one address or a span such as " +
					"`10.51.0.50-10.51.0.59`."},
		},
	})
}

func stringList(desc string) schema.ListAttribute {
	return schema.ListAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: desc}
}
