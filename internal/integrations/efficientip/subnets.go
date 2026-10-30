package efficientip

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
)

// Subnets carved from a block, for the NSX segments rvav makes for a
// deployment (ADR-0028). A block is a non-terminal subnet, named or given
// by its range; what rvav carves from it is a terminal subnet named after
// the segment, so asking again returns it.

// ErrBlockFull is a block with no free range of the size asked.
var ErrBlockFull = errors.New("no free subnet of that size in the block")

// block finds a block by its name or range.
func (c *HTTPClient) block(ctx context.Context, block string) (row, error) {
	where := "is_terminal='0' and subnet_name=" + quote(block)
	if p, err := netip.ParsePrefix(block); err == nil {
		where = "is_terminal='0' and start_hostaddr=" + quote(p.Masked().Addr().String())
	}
	if c.opts.Space != "" {
		where += " and site_name=" + quote(c.opts.Space)
	}
	rows, err := c.call(ctx, http.MethodGet, "ip_block_subnet_list", list(where, 2))
	switch {
	case err != nil:
		return nil, err
	case len(rows) == 0:
		return nil, fmt.Errorf("EfficientIP has no block %s", block)
	case len(rows) > 1:
		return nil, fmt.Errorf("block %s is in several IPAM spaces; set the space rvav uses", block)
	}
	return rows[0], nil
}

func subnetInfo(r row) SubnetInfo {
	params, _ := url.ParseQuery(r["subnet_class_parameters"])
	return SubnetInfo{ID: r["subnet_id"], Name: r["subnet_name"], Space: r["site_name"],
		CIDR: r["start_hostaddr"] + "/" + strconv.Itoa(prefixLen(r)), Params: params}
}

// AllocateSubnet returns the subnet named name in the block, or carves the
// first free range of prefix bits from it, with params as its class
// parameters.
func (c *HTTPClient) AllocateSubnet(ctx context.Context, block string, prefix int, name string, params map[string]string) (SubnetInfo, error) {
	b, err := c.block(ctx, block)
	if err != nil {
		return SubnetInfo{}, err
	}
	mine := func() ([]row, error) {
		return c.call(ctx, http.MethodGet, "ip_block_subnet_list",
			list("parent_subnet_id="+quote(b["subnet_id"])+" and is_terminal='1' and subnet_name="+quote(name), 1))
	}
	if rows, err := mine(); err != nil || len(rows) > 0 {
		if err != nil {
			return SubnetInfo{}, err
		}
		return subnetInfo(rows[0]), nil
	}
	cp := url.Values{}
	for k, v := range params {
		cp.Set(k, v)
	}
	// A range taken between the search and the add is searched again.
	for range 3 {
		free, err := c.call(ctx, http.MethodOptions, "ip_find_free_subnet",
			url.Values{"block_id": {b["subnet_id"]}, "prefix": {strconv.Itoa(prefix)}, "max_find": {"1"}})
		if err != nil {
			return SubnetInfo{}, err
		}
		start := first(free, "start_hostaddr")
		if start == "" {
			return SubnetInfo{}, fmt.Errorf("%s /%d: %w", block, prefix, ErrBlockFull)
		}
		_, err = c.call(ctx, http.MethodPost, "ip_subnet_add", url.Values{"site_id": {b["site_id"]}, "parent_subnet_id": {b["subnet_id"]},
			"subnet_addr": {start}, "subnet_prefix": {strconv.Itoa(prefix)}, "subnet_name": {name}, "is_terminal": {"1"},
			"subnet_class_parameters": {cp.Encode()}})
		if err != nil && !refused(err) {
			return SubnetInfo{}, err
		}
		if rows, err := mine(); err != nil || len(rows) > 0 {
			if err != nil {
				return SubnetInfo{}, err
			}
			return subnetInfo(rows[0]), nil
		}
	}
	return SubnetInfo{}, fmt.Errorf("%s /%d: the free ranges were taken while rvav asked", block, prefix)
}

// DeleteSubnet deletes a subnet rvav carved; one already gone is no error.
func (c *HTTPClient) DeleteSubnet(ctx context.Context, id string) error {
	if gone, err := c.gone(ctx, "ip_block_subnet_list", "subnet_id", id); err != nil || gone {
		return err
	}
	_, err := c.call(ctx, http.MethodDelete, "ip_subnet_delete", url.Values{"subnet_id": {id}})
	return err
}
