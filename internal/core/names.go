package core

import (
	"context"
	"fmt"

	"github.com/damhau/rvav/internal/integrations/efficientip"
)

// maxDNSChecks is how many names one request may look up in DNS before
// giving up: a naming rule whose next hundred names are all in DNS needs
// a person to look at it.
const maxDNSChecks = 100

// nameCheck says whether a VM name is taken: in vCenter, or in DNS under
// the zone the VM's FQDN goes in. DNS answers are kept; when DNS can't be
// asked the name counts as free and err says why.
type nameCheck struct {
	ctx     context.Context
	ipam    efficientip.Client
	vcenter map[string]bool
	zone    string
	dns     map[string]bool
	err     error
}

func (c *nameCheck) taken(name string) bool {
	if c.vcenter[name] {
		return true
	}
	if c.ipam == nil || c.zone == "" || c.err != nil {
		return false
	}
	if taken, ok := c.dns[name]; ok {
		return taken
	}
	if len(c.dns) >= maxDNSChecks {
		c.err = fmt.Errorf("the next %d names are all in DNS under %s", maxDNSChecks, c.zone)
		return false
	}
	exists, err := c.ipam.NameExists(c.ctx, name+"."+c.zone)
	if err != nil {
		c.err = &UpstreamError{System: "EfficientIP", Err: err}
		return false
	}
	if c.dns == nil {
		c.dns = map[string]bool{}
	}
	c.dns[name] = exists
	return exists
}
