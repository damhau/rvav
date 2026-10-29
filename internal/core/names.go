package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

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
	// rvav holds the names of every deployment rvav made, deleted ones too:
	// a name is never given twice (ADR-0020).
	rvav map[string]bool
	zone string
	dns  map[string]bool
	err  error
}

func (c *nameCheck) taken(name string) bool { return c.takenIn(name) != "" }

// takenIn says where a name is taken: vcenter, rvav or dns ("" when free).
func (c *nameCheck) takenIn(name string) string {
	if c.vcenter[name] {
		return "vcenter"
	}
	if c.rvav[name] {
		return "rvav"
	}
	if c.ipam == nil || c.zone == "" || c.err != nil {
		return ""
	}
	if taken, ok := c.dns[name]; ok {
		return map[bool]string{true: "dns"}[taken]
	}
	if len(c.dns) >= maxDNSChecks {
		c.err = &ConflictError{Msg: fmt.Sprintf("The next %d names are all in DNS under %s. Ask an administrator to look at the naming rule.", maxDNSChecks, c.zone)}
		return ""
	}
	exists, err := c.ipam.NameExists(c.ctx, name+"."+c.zone)
	if err != nil {
		c.err = &UpstreamError{System: "EfficientIP", Err: err}
		return ""
	}
	if c.dns == nil {
		c.dns = map[string]bool{}
	}
	c.dns[name] = exists
	return map[bool]string{true: "dns"}[exists]
}

// submitting counts the submissions under way in this process for each
// name prefix.
var submitting = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

// maxPrefetch bounds the names one submission looks up ahead.
const maxPrefetch = 32

// prefetchNames looks up in DNS, before the name counter is locked and all
// at once, the names this request may be given while others are being
// submitted for the same prefix: as many more as there are others. Under
// the lock they are answered from what it found, so requests for one prefix
// don't each wait for the DNS lookups of those before them (S14.7: 20
// submitted together took up to 2.8 s with a SOLIDserver answering in
// 100 ms). Alone, it looks up nothing the evaluation didn't. The returned
// function ends the submission.
func (s *Service) prefetchNames(ctx context.Context, ev *Evaluation) func() {
	parts := ev.allParts()
	var prefixes []string
	others := map[string]int{}
	submitting.Lock()
	for _, p := range parts {
		if p.count > 0 && p.prefix != "" && !slices.Contains(prefixes, p.prefix) {
			prefixes = append(prefixes, p.prefix)
			others[p.prefix] = submitting.n[p.prefix]
			submitting.n[p.prefix]++
		}
	}
	submitting.Unlock()
	done := func() {
		submitting.Lock()
		defer submitting.Unlock()
		for _, prefix := range prefixes {
			if submitting.n[prefix]--; submitting.n[prefix] <= 0 {
				delete(submitting.n, prefix)
			}
		}
	}
	for _, p := range parts {
		c := p.check
		if others[p.prefix] == 0 || c == nil || c.ipam == nil || c.zone == "" || c.err != nil {
			continue
		}
		next, err := s.Store.PeekName(ctx, p.counter)
		if err != nil {
			continue
		}
		var names []string
		for n := next; len(names) < min(p.count*(others[p.prefix]+1), maxPrefetch) && n < next+2*maxPrefetch; n++ {
			name := p.naming.Name(p.prefix, n)
			if _, known := c.dns[name]; !known && !c.vcenter[name] && !c.rvav[name] {
				names = append(names, name)
			}
		}
		c.prefetch(names)
	}
	return done
}

// prefetch looks names up in DNS together (eight at a time) and keeps the
// answers; a name it couldn't ask about is asked again when needed.
func (c *nameCheck) prefetch(names []string) {
	var mu sync.Mutex
	found := map[string]bool{}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			exists, err := c.ipam.NameExists(c.ctx, name+"."+c.zone)
			if err == nil {
				mu.Lock()
				found[name] = exists
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if c.dns == nil {
		c.dns = map[string]bool{}
	}
	maps.Copy(c.dns, found)
}
