package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// Templates and size presets offered to projects and environments (vRA
// image and flavour mappings, ADR-0020).

var (
	templateKey = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	sizeKey     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,15}$`)
)

func presentOffers(list []store.Offer) []api.Offer {
	out := make([]api.Offer, len(list))
	for i, o := range list {
		out[i] = api.Offer{ProjectId: o.ProjectID, Environment: o.Environment}
	}
	return out
}

// checkOffers turns offers into rows, saying which name unknown projects
// or environments.
func (s *Service) checkOffers(ctx context.Context, list *[]api.Offer) ([]store.Offer, []api.FieldError, error) {
	if list == nil {
		return nil, nil, nil
	}
	envs, err := s.Store.ListEnvironments(ctx)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]bool{}
	for _, e := range envs {
		known[e.Key] = true
	}
	var out []store.Offer
	var errs []api.FieldError
	for i, o := range *list {
		row := store.Offer{Environment: o.Environment}
		if o.ProjectId != nil && *o.ProjectId != "" {
			proj, err := s.Store.GetProject(ctx, *o.ProjectId)
			if errors.Is(err, store.ErrNotFound) {
				errs = append(errs, fieldErr(fmt.Sprintf("/offers/%d/project_id", i), "unknown", "There is no such project."))
				continue
			} else if err != nil {
				return nil, nil, err
			}
			row.ProjectID = &proj.ID
		}
		if o.Environment != nil && !known[*o.Environment] {
			errs = append(errs, fieldErr(fmt.Sprintf("/offers/%d/environment", i), "unknown", fmt.Sprintf("%q is not an environment.", *o.Environment)))
			continue
		}
		out = append(out, row)
	}
	return out, errs, nil
}

func (s *Service) VMTemplates(ctx context.Context, p *auth.Principal) ([]api.VmTemplate, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	list, err := s.Store.VMTemplates(ctx)
	if err != nil {
		return nil, err
	}
	offers, err := s.Store.Offers(ctx, "template")
	if err != nil {
		return nil, err
	}
	inventory, err := s.inventoryTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.VmTemplate, len(list))
	for i, t := range list {
		found := inventory[t.VCenterTemplate]
		out[i] = api.VmTemplate{Key: t.Key, Label: t.Label, VcenterTemplate: t.VCenterTemplate, OsFamily: api.VmTemplateOsFamily(t.OSFamily),
			Customization: t.Customization, Description: t.Description, Offers: presentOffers(offers[t.Key]), InInventory: &found}
	}
	return out, nil
}

func (s *Service) inventoryTemplates(ctx context.Context) (map[string]bool, error) {
	list, err := s.Store.InventoryTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, t := range list {
		out[t.Name] = true
	}
	return out, nil
}

func (s *Service) SaveVMTemplate(ctx context.Context, p *auth.Principal, key string, w api.VmTemplateWrite) (api.VmTemplate, error) {
	if !p.IsAdmin() {
		return api.VmTemplate{}, ErrForbidden
	}
	create := key == ""
	var errs []api.FieldError
	if create && !templateKey.MatchString(w.Key) {
		errs = append(errs, fieldErr("/key", "pattern", "2 to 41 lower-case letters, digits and dashes, starting with a letter."))
	}
	if strings.TrimSpace(w.Label) == "" {
		errs = append(errs, fieldErr("/label", "required", "Give the template a label."))
	}
	switch {
	case strings.TrimSpace(w.VcenterTemplate) == "":
		errs = append(errs, fieldErr("/vcenter_template", "required", "Name the template in vCenter."))
	case strings.HasPrefix(w.VcenterTemplate, "library:"):
		errs = append(errs, fieldErr("/vcenter_template", "not_yet", "Content library items come with advanced provisioning (S12): name a vCenter template."))
	}
	if w.OsFamily != api.VmTemplateWriteOsFamilyLinux && w.OsFamily != api.VmTemplateWriteOsFamilyWindows {
		errs = append(errs, fieldErr("/os_family", "enum", "linux or windows."))
	}
	offers, offerErrs, err := s.checkOffers(ctx, w.Offers)
	if err != nil {
		return api.VmTemplate{}, err
	}
	if errs = append(errs, offerErrs...); len(errs) > 0 {
		return api.VmTemplate{}, &ValidationError{Errors: errs}
	}
	if create {
		key = w.Key
	}
	t := store.VMTemplate{Key: key, Label: strings.TrimSpace(w.Label), VCenterTemplate: strings.TrimSpace(w.VcenterTemplate),
		OSFamily: string(w.OsFamily), Customization: deref(w.Customization), Description: deref(w.Description)}
	switch err := s.Store.SaveVMTemplate(ctx, t, offers, create); {
	case errors.Is(err, store.ErrInUse):
		return api.VmTemplate{}, &ConflictError{Msg: "A template has the key " + key + "."}
	case errors.Is(err, store.ErrNotFound):
		return api.VmTemplate{}, ErrNotFound
	case err != nil:
		return api.VmTemplate{}, err
	}
	s.audit(ctx, p, map[bool]string{true: "template.create", false: "template.update"}[create], "vm_template", key, nil, w)
	list, err := s.VMTemplates(ctx, p)
	if err != nil {
		return api.VmTemplate{}, err
	}
	for _, x := range list {
		if x.Key == key {
			return x, nil
		}
	}
	return api.VmTemplate{}, ErrNotFound
}

func (s *Service) DeleteVMTemplate(ctx context.Context, p *auth.Principal, key string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	if err := s.Store.DeleteVMTemplate(ctx, key); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "template.delete", "vm_template", key, nil, nil)
	return nil
}

func (s *Service) SizePresets(ctx context.Context, p *auth.Principal) ([]api.SizePreset, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	list, err := s.Store.SizePresets(ctx)
	if err != nil {
		return nil, err
	}
	offers, err := s.Store.Offers(ctx, "size")
	if err != nil {
		return nil, err
	}
	out := make([]api.SizePreset, len(list))
	for i, z := range list {
		out[i] = api.SizePreset{Key: z.Key, Label: z.Label, Vcpu: z.VCPU, MemoryGb: z.MemoryGB, DiskGb: z.DiskGB,
			Offers: presentOffers(offers[z.Key])}
	}
	return out, nil
}

func (s *Service) SaveSizePreset(ctx context.Context, p *auth.Principal, key string, w api.SizePresetWrite) (api.SizePreset, error) {
	if !p.IsAdmin() {
		return api.SizePreset{}, ErrForbidden
	}
	create := key == ""
	var errs []api.FieldError
	if create && !sizeKey.MatchString(w.Key) {
		errs = append(errs, fieldErr("/key", "pattern", "1 to 16 letters, digits and dashes, for example M or xl-mem."))
	}
	if strings.TrimSpace(w.Label) == "" {
		errs = append(errs, fieldErr("/label", "required", "Give the size a label."))
	}
	for _, f := range []struct {
		ptr      string
		v, lo, h int
	}{{"/vcpu", w.Vcpu, 1, 128}, {"/memory_gb", w.MemoryGb, 1, 2048}, {"/disk_gb", w.DiskGb, 1, 65536}} {
		if f.v < f.lo || f.v > f.h {
			errs = append(errs, fieldErr(f.ptr, "range", fmt.Sprintf("From %d to %d.", f.lo, f.h)))
		}
	}
	offers, offerErrs, err := s.checkOffers(ctx, w.Offers)
	if err != nil {
		return api.SizePreset{}, err
	}
	if errs = append(errs, offerErrs...); len(errs) > 0 {
		return api.SizePreset{}, &ValidationError{Errors: errs}
	}
	if create {
		key = w.Key
	}
	z := store.SizePreset{Key: key, Label: strings.TrimSpace(w.Label), VCPU: w.Vcpu, MemoryGB: w.MemoryGb, DiskGB: w.DiskGb}
	switch err := s.Store.SaveSizePreset(ctx, z, offers, create); {
	case errors.Is(err, store.ErrInUse):
		return api.SizePreset{}, &ConflictError{Msg: "A size has the key " + key + "."}
	case errors.Is(err, store.ErrNotFound):
		return api.SizePreset{}, ErrNotFound
	case err != nil:
		return api.SizePreset{}, err
	}
	s.audit(ctx, p, map[bool]string{true: "size.create", false: "size.update"}[create], "size_preset", key, nil, w)
	list, err := s.SizePresets(ctx, p)
	if err != nil {
		return api.SizePreset{}, err
	}
	for _, x := range list {
		if x.Key == key {
			return x, nil
		}
	}
	return api.SizePreset{}, ErrNotFound
}

func (s *Service) DeleteSizePreset(ctx context.Context, p *auth.Principal, key string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	if err := s.Store.DeleteSizePreset(ctx, key); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "size.delete", "size_preset", key, nil, nil)
	return nil
}

// offeredTemplates are the templates a project and environment may use,
// by key.
func (s *Service) offeredTemplates(ctx context.Context, projectID, environment string) ([]store.VMTemplate, error) {
	keys, err := s.Store.OfferedKeys(ctx, "template", projectID, environment)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	all, err := s.Store.VMTemplates(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.VMTemplate
	for _, t := range all {
		if containsString(keys, t.Key) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Service) offeredSizes(ctx context.Context, projectID, environment string) ([]store.SizePreset, error) {
	keys, err := s.Store.OfferedKeys(ctx, "size", projectID, environment)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	all, err := s.Store.SizePresets(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.SizePreset
	for _, z := range all {
		if containsString(keys, z.Key) {
			out = append(out, z)
		}
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sizeMeta(z store.SizePreset) string {
	return fmt.Sprintf("%d vCPU · %d GB · %d GB", z.VCPU, z.MemoryGB, z.DiskGB)
}
