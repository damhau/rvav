package core

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// Images per vCenter (ADR-0037): a catalogue template has a source in each
// vCenter it can be cloned in. Placement chooses the vCenter and the
// cluster among those, then the VMs are cloned from that vCenter's source.
// A source's vCenter is "" for the vCenter integration's: in the inventory,
// any vCenter that isn't one of those added.

// vcenterSet tells the vCenters added in Administration › vCenters from the
// vCenter integration's.
type vcenterSet map[string]bool

func (s *Service) addedVCenters(ctx context.Context) (vcenterSet, error) {
	rows, err := s.Store.VCenters(ctx)
	if err != nil {
		return nil, err
	}
	out := vcenterSet{}
	for _, v := range rows {
		out[v.Name] = true
	}
	return out, nil
}

// in says a source's vCenter is the inventory's vCenter vc.
func (a vcenterSet) in(source, vc string) bool {
	return source == vc || source == "" && !a[vc]
}

// sourceIn is the template's source in the inventory's vCenter vc.
func sourceIn(t store.VMTemplate, vc string, added vcenterSet) (store.TemplateSource, bool) {
	if i := slices.IndexFunc(t.Sources, func(src store.TemplateSource) bool { return src.VCenter == vc }); i >= 0 {
		return t.Sources[i], true
	}
	if !added[vc] {
		if i := slices.IndexFunc(t.Sources, func(src store.TemplateSource) bool { return src.VCenter == "" }); i >= 0 {
			return t.Sources[i], true
		}
	}
	return store.TemplateSource{}, false
}

// hasSource says the template has a source naming this vCenter template or
// content library item.
func hasSource(t store.VMTemplate, template, library string) bool {
	return slices.ContainsFunc(t.Sources, func(src store.TemplateSource) bool {
		return src.Template == template && src.Library == library
	})
}

// inInventory says the source's vCenter has it now.
func inInventory(src store.TemplateSource, inv []store.InvTemplate, added vcenterSet) bool {
	return slices.ContainsFunc(inv, func(t store.InvTemplate) bool {
		return t.Name == src.Template && t.Library == src.Library && added.in(src.VCenter, t.VCenter)
	})
}

// vcenterNames lists vCenters for messages, the vCenter integration's
// without a name included.
func vcenterNames(list []string) string {
	if len(list) == 1 {
		return strings.TrimSpace("vCenter " + list[0])
	}
	names := make([]string, len(list))
	for i, vc := range list {
		names[i] = cmp.Or(vc, "the vCenter integration's")
	}
	return "vCenters " + strings.Join(names, ", ")
}

// sourceText names a source for messages: its template, and its content
// library.
func sourceText(src store.TemplateSource) string {
	if src.Library != "" {
		return src.Template + " in content library " + src.Library
	}
	return src.Template
}

// checkSources checks a template's sources as written, and gives them as
// stored: the vCenter integration's as "".
func (s *Service) checkSources(ctx context.Context, list []api.VmTemplateSourceWrite) ([]store.TemplateSource, []api.FieldError, error) {
	var errs []api.FieldError
	if len(list) == 0 {
		return nil, []api.FieldError{fieldErr("/sources", "required", "Name the template in a vCenter at least.")}, nil
	}
	added, err := s.addedVCenters(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]store.TemplateSource, 0, len(list))
	seen := map[string]bool{}
	for i, w := range list {
		at := fmt.Sprintf("/sources/%d", i)
		src := store.TemplateSource{VCenter: strings.TrimSpace(w.Vcenter), Template: strings.TrimSpace(w.Template),
			Library: strings.TrimSpace(deref(w.ContentLibrary))}
		if src.VCenter == s.Config.VCenter.Name {
			src.VCenter = ""
		}
		switch {
		case src.VCenter != "" && !added[src.VCenter]:
			errs = append(errs, fieldErr(at+"/vcenter", "unknown", "rvav manages no vCenter "+src.VCenter+"."))
		case seen[src.VCenter]:
			errs = append(errs, fieldErr(at+"/vcenter", "duplicate", "A template has one source per vCenter: this one has two."))
		}
		seen[src.VCenter] = true
		switch {
		case src.Template == "":
			errs = append(errs, fieldErr(at+"/template", "required", "Name the template in this vCenter."))
		case strings.HasPrefix(src.Template, "library:"):
			errs = append(errs, fieldErr(at+"/template", "invalid", "Name the content library item, and its library in Content library."))
		}
		out = append(out, src)
	}
	return out, errs, nil
}

// presentSources gives the sources with their vCenter's name, and whether
// it has them now.
func (s *Service) presentSources(t store.VMTemplate, inv []store.InvTemplate, added vcenterSet) []api.VmTemplateSource {
	out := make([]api.VmTemplateSource, len(t.Sources))
	for i, src := range t.Sources {
		found := inInventory(src, inv, added)
		out[i] = api.VmTemplateSource{Vcenter: src.VCenter, Template: src.Template, ContentLibrary: src.Library, InInventory: &found}
		if src.VCenter == "" {
			out[i].Vcenter = s.Config.VCenter.Name
		}
	}
	return out
}
