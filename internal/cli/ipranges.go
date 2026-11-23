package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func (a *app) ipRangesCmd() *cobra.Command {
	c := group("ip-ranges", "rvav's IP ranges, for networks without EfficientIP, and who holds each address")
	c.AddCommand(a.ipRangesListCmd(), a.ipRangesShowCmd(), a.ipRangesReleaseCmd())
	return c
}

func (a *app) ipRanges(ctx context.Context, c *rvavclient.ClientWithResponses) (*rvavclient.ListIpRangesResponse, error) {
	r, err := c.ListIpRangesWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	return r, rvavclient.Check(r.HTTPResponse, r.Body)
}

func (a *app) ipRangesListCmd() *cobra.Command {
	var network string
	c := &cobra.Command{
		Use:   "list",
		Short: "The IP ranges, with their free addresses",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			r, err := a.ipRanges(ctx, c)
			if err != nil {
				return err
			}
			raw, err := rawItems(r.Body)
			if err != nil {
				return err
			}
			var items []rvavclient.IpRange
			var shown []json.RawMessage
			for i, x := range r.JSON200.Items {
				if network == "" || strings.EqualFold(x.Network, network) {
					items, shown = append(items, x), append(shown, raw[i])
				}
			}
			return a.list(shown, "No IP ranges.", func() error {
				t := newTable(a.Out, "ID", "NAME", "VCENTER", "NETWORK", "ADDRESSES", "FREE")
				for _, x := range items {
					t.row(x.Id, x.Name, x.Vcenter, x.Network, x.StartAddress+"-"+x.EndAddress, fmt.Sprintf("%d of %d", x.Free, x.Size))
				}
				return t.flush()
			})
		},
	}
	c.Flags().StringVar(&network, "network", "", "only the ranges of this port group or NSX segment")
	return c
}

// ipRange is the range named or with this ID, and the API's JSON of it.
func (a *app) ipRange(ctx context.Context, c *rvavclient.ClientWithResponses, ref string) (rvavclient.IpRange, json.RawMessage, error) {
	r, err := a.ipRanges(ctx, c)
	if err != nil {
		return rvavclient.IpRange{}, nil, err
	}
	raw, err := rawItems(r.Body)
	if err != nil {
		return rvavclient.IpRange{}, nil, err
	}
	var named []int
	for i, x := range r.JSON200.Items {
		if x.Id == ref {
			return x, raw[i], nil
		}
		if strings.EqualFold(x.Name, ref) {
			named = append(named, i)
		}
	}
	switch len(named) {
	case 0:
		return rvavclient.IpRange{}, nil, fmt.Errorf("no IP range %q: rvavctl ip-ranges list shows them", ref)
	case 1:
		return r.JSON200.Items[named[0]], raw[named[0]], nil
	}
	return rvavclient.IpRange{}, nil, fmt.Errorf("%d IP ranges are named %q: give the one's ID", len(named), ref)
}

func (a *app) ipRangesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show RANGE",
		Short: "An IP range, by name or ID, and the VM that holds each address it gave",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			rg, raw, err := a.ipRange(ctx, c, args[0])
			if err != nil {
				return err
			}
			given, err := c.ListIpAllocationsWithResponse(ctx, rg.Id)
			if err != nil {
				return err
			}
			if err := rvavclient.Check(given.HTTPResponse, given.Body); err != nil {
				return err
			}
			if a.jsonOut() {
				items, err := rawItems(given.Body)
				if err != nil {
					return err
				}
				if items == nil {
					items = []json.RawMessage{}
				}
				return a.printJSON(map[string]any{"range": raw, "addresses": items})
			}
			return a.printIPRange(rg, given.JSON200.Items)
		},
	}
}

func skipped(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 address: a VM holds it that the range didn't give it to, or a DNS server is on it"
	}
	return fmt.Sprintf("%d addresses: VMs hold them that the range didn't give them to, or DNS servers are on them", n)
}

func (a *app) printIPRange(x rvavclient.IpRange, given []rvavclient.IpAllocation) error {
	if err := details(a.Out, "Range", x.Name+" ("+x.Id+")", "Description", str(x.Description),
		"Network", x.Network+" in "+x.Vcenter, "Subnet", x.Cidr+" via "+x.Gateway,
		"Addresses", x.StartAddress+" to "+x.EndAddress, "Excluded", strings.Join(x.Excluded, ", "),
		"Free", fmt.Sprintf("%d of %d", x.Free, x.Size), "Skipped", skipped(x.Taken), "DNS servers", strings.Join(x.DnsServers, ", "),
		"Search domains", strings.Join(x.DnsSearchDomains, ", "), "DNS domain", x.DnsDomain,
		"DNS records", "none: rvav writes no DNS records for its ranges"); err != nil {
		return err
	}
	if len(given) == 0 {
		_, err := fmt.Fprintln(a.Out, "\nNo address given yet.")
		return err
	}
	fmt.Fprintln(a.Out)
	t := newTable(a.Out, "ADDRESS", "VM", "ADAPTER", "DEPLOYMENT", "GIVEN")
	for _, g := range given {
		deployment := str(g.DeploymentName)
		if deployment == "" {
			deployment = "(none)"
		}
		t.row(g.Address, g.Hostname, str(g.Adapter), deployment, when(&g.AllocatedAt))
	}
	return t.flush()
}

func (a *app) ipRangesReleaseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "release RANGE ADDRESS",
		Short: "Give back an address a range gave a VM rvav no longer has, such as an unregistered VM's",
		Long: `Gives back an address that the reconcile report lists as given to a VM
rvav no longer has, so that the range can give it again. rvav refuses an
address a deployment's VM still holds: delete the VM, or move its adapter.
If the VM still runs on the address in vCenter, exclude the address from
the range instead.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			rg, _, err := a.ipRange(ctx, c, args[0])
			if err != nil {
				return err
			}
			r, err := c.ReleaseIpAllocationWithResponse(ctx, rg.Id, args[1])
			if err != nil {
				return err
			}
			if err := rvavclient.Check(r.HTTPResponse, r.Body); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.Err, "%s given back to %s.\n", args[1], rg.Name)
			return err
		},
	}
}
