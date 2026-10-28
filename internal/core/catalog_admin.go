package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Catalog administration (ADR-0019): items and their versions, for catalog
// admins and administrators.

func (s *Service) adminItem(ctx context.Context, p *auth.Principal, idOrSlug string) (store.AdminItem, error) {
	if !canAdminCatalog(p) {
		return store.AdminItem{}, ErrForbidden
	}
	it, err := s.Store.GetAdminItem(ctx, idOrSlug)
	if errors.Is(err, store.ErrNotFound) {
		return it, ErrNotFound
	}
	return it, err
}

func presentVersion(v store.VersionRow) api.CatalogItemVersion {
	out := api.CatalogItemVersion{Id: v.ID, Number: v.Number, State: api.CatalogItemVersionState(v.State), Changelog: v.Changelog,
		CreatedAt: v.CreatedAt, PublishedAt: v.PublishedAt, DeprecatedAt: v.DeprecatedAt, Deployments: v.Deployments,
		Revision: v.Revision}
	if v.CreatedBy != nil {
		out.CreatedBy = &api.UserRef{Id: *v.CreatedBy, Name: deref(v.CreatedByName)}
	}
	return out
}

// AdminCatalogItem is an item with its versions and entitlements.
func (s *Service) AdminCatalogItem(ctx context.Context, p *auth.Principal, idOrSlug string) (api.AdminCatalogItem, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	return s.presentAdminItem(ctx, it)
}

func (s *Service) presentAdminItem(ctx context.Context, it store.AdminItem) (api.AdminCatalogItem, error) {
	out := api.AdminCatalogItem{Id: it.ID, Slug: it.Slug, Name: it.Name, Summary: it.Summary, DescriptionMd: it.DescriptionMD,
		Category: it.Category, Type: api.CatalogItemType(it.Type), Icon: it.Icon, Tags: append([]string{}, it.Tags...),
		Featured: it.Featured, Requested: it.Requested, CreatedAt: it.CreatedAt, Versions: []api.CatalogItemVersion{}}
	out.Source.Kind = api.AdminCatalogItemSourceKind(it.SourceKind)
	out.Source.AwxTemplateId, out.Source.AwxTemplateName = it.AWXTemplateID, it.AWXTemplateName
	versions, err := s.Store.ItemVersions(ctx, it.ID)
	if err != nil {
		return out, err
	}
	for _, v := range versions {
		pv := presentVersion(v)
		out.Versions = append(out.Versions, pv)
		switch v.State {
		case "published":
			out.PublishedVersion = present.Ptr(pv)
		case "draft":
			out.DraftVersion = present.Ptr(pv)
		}
	}
	ent, err := s.entitlements(ctx, it)
	out.Entitlements = ent
	return out, err
}

func (s *Service) entitlements(ctx context.Context, it store.AdminItem) (api.CatalogEntitlements, error) {
	ids, err := s.Store.ItemProjects(ctx, it.ID)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	out := api.CatalogEntitlements{AllProjects: it.AllProjects, ProjectIds: append([]string{}, ids...)}
	all, err := s.Store.ListProjects(ctx, nil, 500, nil)
	if err != nil {
		return out, err
	}
	projects := make([]struct {
		Code string `json:"code"`
		Id   string `json:"id"`
		Name string `json:"name"`
	}, len(all.Items))
	for i, p := range all.Items {
		projects[i].Code, projects[i].Id, projects[i].Name = p.Code, p.ID, p.Name
	}
	out.Projects = &projects
	return out, nil
}

// AdminCatalogItems lists every item, in any state.
func (s *Service) AdminCatalogItems(ctx context.Context, p *auth.Principal, f store.AdminItemFilter) (api.AdminCatalogItemPage, error) {
	out := api.AdminCatalogItemPage{Items: []api.AdminCatalogItem{}}
	if !canAdminCatalog(p) {
		return out, ErrForbidden
	}
	page, err := s.Store.AdminItems(ctx, f)
	if err != nil {
		return out, err
	}
	for _, it := range page.Items {
		a, err := s.presentAdminItem(ctx, it)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, a)
	}
	if page.Next != nil {
		out.NextCursor = present.Ptr(page.Next.Encode())
	}
	return out, nil
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// CreateCatalogItem makes an item and its first draft.
func (s *Service) CreateCatalogItem(ctx context.Context, p *auth.Principal, c api.CatalogItemCreate) (api.AdminCatalogItem, error) {
	if !canAdminCatalog(p) {
		return api.AdminCatalogItem{}, ErrForbidden
	}
	var errs []api.FieldError
	if !slugPattern.MatchString(c.Slug) {
		errs = append(errs, fieldErr("/slug", "pattern", "Lower case letters, digits and dashes, 2 to 63 characters."))
	}
	if strings.TrimSpace(c.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the item a name."))
	}
	if strings.TrimSpace(c.Summary) == "" {
		errs = append(errs, fieldErr("/summary", "required", "Say in a line what the item gives."))
	}
	if ok, err := s.Store.CategoryExists(ctx, c.Category); err != nil {
		return api.AdminCatalogItem{}, err
	} else if !ok {
		errs = append(errs, fieldErr("/category", "unknown", fmt.Sprintf("There is no category %q.", c.Category)))
	}
	icon := orDefault(deref(c.Icon), defaultIcon(string(c.Type)))
	if !slices.Contains(CatalogIcons, icon) {
		errs = append(errs, fieldErr("/icon", "unknown", fmt.Sprintf("%q is not one of rvav's icons.", icon)))
	}
	def := starterDefinition(string(c.Type), c.Name)
	if c.Definition != nil {
		def = *c.Definition
	}
	errs = append(errs, checkDefinition(string(c.Type), def, "/definition", false)...)
	refs, err := s.checkSourceRefs(ctx, def, "/definition")
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	if errs = append(errs, refs...); len(errs) > 0 {
		return api.AdminCatalogItem{}, &ValidationError{Errors: errs}
	}
	it := store.NewItem{ID: ids.New(ids.Item), Slug: c.Slug, Name: strings.TrimSpace(c.Name), Summary: strings.TrimSpace(c.Summary),
		DescriptionMD: deref(c.DescriptionMd), Category: c.Category, Type: string(c.Type), Icon: icon, Featured: c.Featured != nil && *c.Featured}
	if c.Tags != nil {
		it.Tags = *c.Tags
	}
	v := store.NewVersion{ID: ids.New(ids.Version), Number: "1.0", CreatedBy: p.User.ID, Def: storeDef(def)}
	if err := s.Store.CreateItem(ctx, it, v); err != nil {
		if errors.Is(err, store.ErrInUse) {
			return api.AdminCatalogItem{}, &ConflictError{Msg: fmt.Sprintf("The slug %q is taken.", c.Slug)}
		}
		return api.AdminCatalogItem{}, err
	}
	s.auditCatalog(ctx, p, "catalog.item.create", it.ID, map[string]any{"slug": it.Slug, "name": it.Name})
	return s.AdminCatalogItem(ctx, p, it.ID)
}

// UpdateCatalogItem changes an item's metadata.
func (s *Service) UpdateCatalogItem(ctx context.Context, p *auth.Principal, idOrSlug string, u api.CatalogItemUpdate) (api.AdminCatalogItem, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	var errs []api.FieldError
	if u.Slug != nil && !slugPattern.MatchString(*u.Slug) {
		errs = append(errs, fieldErr("/slug", "pattern", "Lower case letters, digits and dashes, 2 to 63 characters."))
	}
	if u.Name != nil && strings.TrimSpace(*u.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the item a name."))
	}
	if u.Summary != nil && strings.TrimSpace(*u.Summary) == "" {
		errs = append(errs, fieldErr("/summary", "required", "Say in a line what the item gives."))
	}
	if u.Category != nil {
		if ok, err := s.Store.CategoryExists(ctx, *u.Category); err != nil {
			return api.AdminCatalogItem{}, err
		} else if !ok {
			errs = append(errs, fieldErr("/category", "unknown", fmt.Sprintf("There is no category %q.", *u.Category)))
		}
	}
	if u.Icon != nil && !slices.Contains(CatalogIcons, *u.Icon) {
		errs = append(errs, fieldErr("/icon", "unknown", fmt.Sprintf("%q is not one of rvav's icons.", *u.Icon)))
	}
	if len(errs) > 0 {
		return api.AdminCatalogItem{}, &ValidationError{Errors: errs}
	}
	err = s.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{Slug: u.Slug, Name: u.Name, Summary: u.Summary,
		DescriptionMD: u.DescriptionMd, Category: u.Category, Icon: u.Icon, Tags: u.Tags, Featured: u.Featured})
	if errors.Is(err, store.ErrInUse) {
		return api.AdminCatalogItem{}, &ConflictError{Msg: fmt.Sprintf("The slug %q is taken.", deref(u.Slug))}
	}
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	s.auditCatalog(ctx, p, "catalog.item.update", it.ID, map[string]any{"changes": u})
	return s.AdminCatalogItem(ctx, p, it.ID)
}

// DeleteCatalogItem deletes an item nobody requested.
func (s *Service) DeleteCatalogItem(ctx context.Context, p *auth.Principal, idOrSlug string) error {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteItem(ctx, it.ID); errors.Is(err, store.ErrInUse) {
		return &ConflictError{Msg: it.Name + " was requested: withdraw it instead (deprecate its published version)."}
	} else if err != nil {
		return err
	}
	s.auditCatalog(ctx, p, "catalog.item.delete", it.ID, map[string]any{"slug": it.Slug, "name": it.Name})
	return nil
}

// itemVersion finds a version of the item.
func (s *Service) itemVersion(ctx context.Context, it store.AdminItem, versionID string) (store.ItemVersion, store.VersionRow, error) {
	v, err := s.Store.GetVersion(ctx, versionID)
	if errors.Is(err, store.ErrNotFound) || err == nil && v.ItemID != it.ID {
		return v, store.VersionRow{}, ErrNotFound
	}
	if err != nil {
		return v, store.VersionRow{}, err
	}
	row, err := s.Store.GetVersionRow(ctx, versionID)
	return v, row, err
}

func presentVersionDetail(v store.ItemVersion, row store.VersionRow) (api.CatalogItemVersionDetail, error) {
	pv := presentVersion(row)
	out := api.CatalogItemVersionDetail{Id: pv.Id, Number: pv.Number, State: api.CatalogItemVersionDetailState(pv.State),
		Changelog: pv.Changelog, CreatedAt: pv.CreatedAt, CreatedBy: pv.CreatedBy, PublishedAt: pv.PublishedAt,
		DeprecatedAt: pv.DeprecatedAt, Deployments: pv.Deployments, Revision: pv.Revision}
	def, err := apiDef(v)
	out.Definition = def
	return out, err
}

// CatalogItemVersion is one version with its definition.
func (s *Service) CatalogItemVersion(ctx context.Context, p *auth.Principal, idOrSlug, versionID string) (api.CatalogItemVersionDetail, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	v, row, err := s.itemVersion(ctx, it, versionID)
	if err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	return presentVersionDetail(v, row)
}

// CreateCatalogItemVersion starts a draft: a copy of a version (the
// published one by default) or a given definition.
func (s *Service) CreateCatalogItemVersion(ctx context.Context, p *auth.Principal, idOrSlug string, c api.CatalogItemVersionCreate) (api.CatalogItemVersionDetail, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	var def api.CatalogVersionDefinition
	switch {
	case c.Definition != nil:
		def = *c.Definition
	case c.FromVersionId != nil || it.PublishedVersionID != nil:
		from := deref(c.FromVersionId)
		if from == "" {
			from = *it.PublishedVersionID
		}
		v, _, err := s.itemVersion(ctx, it, from)
		if errors.Is(err, ErrNotFound) {
			return api.CatalogItemVersionDetail{}, &ValidationError{Errors: []api.FieldError{fieldErr("/from_version_id", "unknown", "This item has no such version.")}}
		}
		if err != nil {
			return api.CatalogItemVersionDetail{}, err
		}
		if def, err = apiDef(v); err != nil {
			return api.CatalogItemVersionDetail{}, err
		}
	default:
		def = starterDefinition(it.Type, it.Name)
	}
	errs := checkDefinition(it.Type, def, "/definition", false)
	refs, err := s.checkSourceRefs(ctx, def, "/definition")
	if err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	if errs = append(errs, refs...); len(errs) > 0 {
		return api.CatalogItemVersionDetail{}, &ValidationError{Errors: errs}
	}
	versions, err := s.Store.ItemVersions(ctx, it.ID)
	if err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	number := deref(c.Number)
	if number == "" {
		number = nextNumber(versions)
	}
	v := store.NewVersion{ID: ids.New(ids.Version), ItemID: it.ID, Number: number, Changelog: deref(c.Changelog),
		CreatedBy: p.User.ID, Def: storeDef(def)}
	if err := s.Store.CreateDraft(ctx, v); errors.Is(err, store.ErrDraftExists) {
		return api.CatalogItemVersionDetail{}, &ConflictError{Msg: it.Name + " already has a draft: change it, or discard it first."}
	} else if errors.Is(err, store.ErrNumberTaken) {
		return api.CatalogItemVersionDetail{}, numberTaken(number)
	} else if err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	s.auditCatalog(ctx, p, "catalog.version.create", it.ID, map[string]any{"version": number, "from": deref(c.FromVersionId)})
	return s.CatalogItemVersion(ctx, p, it.ID, v.ID)
}

// UpdateCatalogItemVersion saves a draft.
func (s *Service) UpdateCatalogItemVersion(ctx context.Context, p *auth.Principal, idOrSlug, versionID string, u api.CatalogItemVersionUpdate) (api.CatalogItemVersionDetail, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	if _, _, err := s.itemVersion(ctx, it, versionID); err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	errs := checkDefinition(it.Type, u.Definition, "/definition", false)
	refs, err := s.checkSourceRefs(ctx, u.Definition, "/definition")
	if err != nil {
		return api.CatalogItemVersionDetail{}, err
	}
	if errs = append(errs, refs...); len(errs) > 0 {
		return api.CatalogItemVersionDetail{}, &ValidationError{Errors: errs}
	}
	_, err = s.Store.SaveDraft(ctx, versionID, u.Number, u.Changelog, storeDef(u.Definition), u.BaseRevision)
	switch {
	case errors.Is(err, store.ErrVersionState):
		return api.CatalogItemVersionDetail{}, &ConflictError{Msg: "Only a draft can change: make a draft from this version."}
	case errors.Is(err, store.ErrVersionChanged):
		return api.CatalogItemVersionDetail{}, &ConflictError{Msg: "This draft was saved elsewhere since you opened it: reload it to see those changes."}
	case errors.Is(err, store.ErrNumberTaken):
		return api.CatalogItemVersionDetail{}, numberTaken(deref(u.Number))
	case err != nil:
		return api.CatalogItemVersionDetail{}, err
	}
	s.auditCatalog(ctx, p, "catalog.version.save", it.ID, map[string]any{"version_id": versionID})
	return s.CatalogItemVersion(ctx, p, it.ID, versionID)
}

// DeleteCatalogItemVersion discards a draft.
func (s *Service) DeleteCatalogItemVersion(ctx context.Context, p *auth.Principal, idOrSlug, versionID string) error {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return err
	}
	if _, _, err := s.itemVersion(ctx, it, versionID); err != nil {
		return err
	}
	if err := s.Store.DeleteDraft(ctx, versionID); errors.Is(err, store.ErrVersionState) {
		return &ConflictError{Msg: "Only a draft can be discarded."}
	} else if err != nil {
		return err
	}
	s.auditCatalog(ctx, p, "catalog.version.delete", it.ID, map[string]any{"version_id": versionID})
	return nil
}

// PublishCatalogItemVersion publishes a draft, or an older version again.
func (s *Service) PublishCatalogItemVersion(ctx context.Context, p *auth.Principal, idOrSlug, versionID string, changelog *string) (api.AdminCatalogItem, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	v, row, err := s.itemVersion(ctx, it, versionID)
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	def, err := apiDef(v)
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	errs := checkDefinition(it.Type, def, "/definition", true)
	refs, err := s.checkSourceRefs(ctx, def, "/definition")
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	if errs = append(errs, refs...); len(errs) > 0 {
		return api.AdminCatalogItem{}, &ValidationError{Errors: errs}
	}
	// The revision checked above must be the one published.
	if err := s.Store.PublishVersion(ctx, it.ID, versionID, changelog, row.Revision); errors.Is(err, store.ErrVersionState) {
		return api.AdminCatalogItem{}, &ConflictError{Msg: "Version " + row.Number + " is already published."}
	} else if errors.Is(err, store.ErrVersionChanged) {
		return api.AdminCatalogItem{}, &ConflictError{Msg: "Draft " + row.Number + " was saved while it was being published: publish it again."}
	} else if err != nil {
		return api.AdminCatalogItem{}, err
	}
	action := "catalog.version.publish"
	if row.State == "deprecated" {
		action = "catalog.version.rollback"
	}
	s.auditCatalog(ctx, p, action, it.ID, map[string]any{"version": row.Number, "version_id": versionID})
	return s.AdminCatalogItem(ctx, p, it.ID)
}

// DeprecateCatalogItemVersion withdraws the published version.
func (s *Service) DeprecateCatalogItemVersion(ctx context.Context, p *auth.Principal, idOrSlug, versionID string) (api.AdminCatalogItem, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	_, row, err := s.itemVersion(ctx, it, versionID)
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	if err := s.Store.DeprecateVersion(ctx, it.ID, versionID); errors.Is(err, store.ErrVersionState) {
		return api.AdminCatalogItem{}, &ConflictError{Msg: "Only the published version can be deprecated."}
	} else if err != nil {
		return api.AdminCatalogItem{}, err
	}
	s.auditCatalog(ctx, p, "catalog.version.deprecate", it.ID, map[string]any{"version": row.Number, "version_id": versionID})
	return s.AdminCatalogItem(ctx, p, it.ID)
}

// CompareCatalogItemVersions says what changed from one version to another.
func (s *Service) CompareCatalogItemVersions(ctx context.Context, p *auth.Principal, idOrSlug, fromID, toID string) (api.CatalogVersionDiff, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogVersionDiff{}, err
	}
	from, fromRow, err := s.itemVersion(ctx, it, fromID)
	if err != nil {
		return api.CatalogVersionDiff{}, err
	}
	to, toRow, err := s.itemVersion(ctx, it, toID)
	if err != nil {
		return api.CatalogVersionDiff{}, err
	}
	out := api.CatalogVersionDiff{From: presentVersion(fromRow), To: presentVersion(toRow), Changes: []api.CatalogVersionChange{}}
	for _, area := range []struct {
		name     api.CatalogVersionChangeArea
		from, to json.RawMessage
	}{{api.CatalogVersionChangeAreaFormSchema, from.FormSchema, to.FormSchema}, {api.CatalogVersionChangeAreaUiSchema, from.UISchema, to.UISchema},
		{api.CatalogVersionChangeAreaInitialValues, from.InitialValues, to.InitialValues}, {api.CatalogVersionChangeAreaBlueprint, from.Blueprint, to.Blueprint}} {
		var a, b any
		_ = json.Unmarshal(area.from, &a)
		_ = json.Unmarshal(area.to, &b)
		for _, c := range diffJSON("", a, b) {
			c.Area = area.name
			out.Changes = append(out.Changes, c)
		}
	}
	return out, nil
}

func (s *Service) auditCatalog(ctx context.Context, p *auth.Principal, action, itemID string, detail map[string]any) {
	if err := s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: &p.User.ID, Action: action, TargetType: "catalog_item",
		TargetID: itemID, Detail: detail}); err != nil {
		s.Log.Error("audit", "action", action, "err", err)
	}
}

func storeDef(d api.CatalogVersionDefinition) store.VersionDef {
	j := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	return store.VersionDef{FormSchema: j(d.FormSchema), UISchema: j(d.UiSchema), InitialValues: j(orEmpty(d.InitialValues)),
		Blueprint: j(orEmpty(d.Blueprint))}
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func apiDef(v store.ItemVersion) (api.CatalogVersionDefinition, error) {
	var d api.CatalogVersionDefinition
	for _, part := range []struct {
		raw json.RawMessage
		to  any
	}{{v.FormSchema, &d.FormSchema}, {v.UISchema, &d.UiSchema}, {v.InitialValues, &d.InitialValues}, {v.Blueprint, &d.Blueprint}} {
		if len(part.raw) == 0 {
			continue
		}
		if err := json.Unmarshal(part.raw, part.to); err != nil {
			return d, err
		}
	}
	d.InitialValues, d.Blueprint = orEmpty(d.InitialValues), orEmpty(d.Blueprint)
	if d.FormSchema == nil {
		d.FormSchema = map[string]any{}
	}
	if d.UiSchema.Fields == nil {
		d.UiSchema.Fields = map[string]api.UiField{}
	}
	if d.UiSchema.Layout.Sections == nil {
		d.UiSchema.Layout.Sections = []api.UiSection{}
	}
	return d, nil
}

// nextNumber is the number after the newest version's: "3.2" gives "3.3",
// "AWX #47" gives "AWX #47.1".
func numberTaken(number string) error {
	return &ValidationError{Errors: []api.FieldError{fieldErr("/number", "taken", "The item already has a version "+number+".")}}
}

// nextNumber follows the highest major.minor number (3.2 gives 3.3),
// skipping numbers in use.
func nextNumber(versions []store.VersionRow) string {
	if len(versions) == 0 {
		return "1.0"
	}
	taken := map[string]bool{}
	bestMajor, bestMinor, found := 0, 0, false
	for _, v := range versions {
		taken[v.Number] = true
		major, minor, ok := strings.Cut(v.Number, ".")
		if !ok {
			major, minor = v.Number, "0"
		}
		ma, err1 := strconv.Atoi(major)
		mi, err2 := strconv.Atoi(minor)
		if err1 != nil || err2 != nil {
			continue
		}
		if !found || ma > bestMajor || ma == bestMajor && mi > bestMinor {
			bestMajor, bestMinor, found = ma, mi, true
		}
	}
	if found {
		for n := bestMinor + 1; ; n++ {
			if next := strconv.Itoa(bestMajor) + "." + strconv.Itoa(n); !taken[next] {
				return next
			}
		}
	}
	last := versions[0].Number
	for i := 1; ; i++ { // no number parses: AWX #47.1, .2, ...
		next := last + "." + strconv.Itoa(i)
		if !slices.ContainsFunc(versions, func(v store.VersionRow) bool { return v.Number == next }) {
			return next
		}
	}
}

// itemKinds is the blueprint kind each item type needs.
var itemKinds = map[string]string{"vm_blueprint": blueprint.KindVM, "awx_job_template": blueprint.KindAWX, "awx_workflow": blueprint.KindAWX,
	"workflow": blueprint.KindWorkflow, "composite": blueprint.KindComposite}

// builtinSources are the builtin value sources (docs: ValueSource in the spec).
var builtinSources = []string{"vsphere.clusters", "vsphere.datastores", "vsphere.networks", "vsphere.templates", "vsphere.folders",
	"ipam.subnets", "ipam.next_free_address", "dns.zones", "awx.inventories", "awx.credentials", "naming.preview",
	"rvav.templates", "rvav.sizes"}

// checkDefinition checks a version's definition as a whole, with pointers
// under prefix. A draft may be incomplete (no template named yet); to
// publish, it must be complete.
func checkDefinition(itemType string, d api.CatalogVersionDefinition, prefix string, publish bool) []api.FieldError {
	var errs []api.FieldError
	add := func(ptr, code, msg string) { errs = append(errs, fieldErr(prefix+ptr, code, msg)) }

	raw, _ := json.Marshal(d.FormSchema)
	if _, err := compileSchemaUncached(raw); err != nil {
		add("/form_schema", "invalid", "The JSON Schema doesn't compile: "+err.Error())
	}
	props, _ := d.FormSchema["properties"].(map[string]any)
	if d.FormSchema["type"] != "object" {
		add("/form_schema/type", "invalid", `The form's schema must be of type "object".`)
	}
	required, _ := d.FormSchema["required"].([]any)
	for i, r := range required {
		if name, _ := r.(string); props[name] == nil {
			add(fmt.Sprintf("/form_schema/required/%d", i), "unknown_input",
				fmt.Sprintf("%v is required but is not an input of the form's schema, so no request could pass.", r))
		}
	}
	placed := map[string]string{}
	for i, sec := range d.UiSchema.Layout.Sections {
		if strings.TrimSpace(sec.Key) == "" {
			add(fmt.Sprintf("/ui_schema/layout/sections/%d/key", i), "required", "Give the section a key.")
		}
		for j, f := range sec.Fields {
			ptr := fmt.Sprintf("/ui_schema/layout/sections/%d/fields/%d", i, j)
			if _, ok := d.UiSchema.Fields[f]; !ok {
				add(ptr, "unknown_field", fmt.Sprintf("The section names %q, which has no UI field.", f))
			}
			if other, ok := placed[f]; ok {
				add(ptr, "duplicate", fmt.Sprintf("%q is already in section %q.", f, other))
			}
			placed[f] = sec.Key
		}
	}
	for _, name := range sortedKeys(d.UiSchema.Fields) {
		f := d.UiSchema.Fields[name]
		ptr := "/ui_schema/fields/" + name
		if _, ok := props[name]; !ok && f.Widget != api.UiWidgetInfo {
			add(ptr, "unknown_input", fmt.Sprintf("%q is not an input of the form's schema.", name))
		}
		if _, ok := placed[name]; !ok {
			add(ptr, "not_placed", fmt.Sprintf("%q is in no section, so it would never show.", name))
		}
		if strings.TrimSpace(f.Label) == "" {
			add(ptr+"/label", "required", "Give the field a label.")
		}
		if f.Rules != nil {
			for rule, expr := range map[string]*string{"visible": f.Rules.Visible, "required": f.Rules.Required,
				"read_only": f.Rules.ReadOnly, "value": f.Rules.Value} {
				if expr != nil && *expr != "" {
					if err := policy.Check(*expr); err != nil {
						add(ptr+"/rules/"+rule, "invalid", "This rule is not valid CEL: "+err.Error())
					}
				}
			}
		}
		if src := f.ValueSource; src != nil {
			errs = append(errs, checkValueSource(*src, prefix+ptr+"/value_source")...)
		}
	}
	for _, name := range sortedKeys(props) {
		if _, ok := d.UiSchema.Fields[name]; !ok {
			add("/form_schema/properties/"+name, "no_ui_field", fmt.Sprintf("%q has no UI field, so nobody could fill it in.", name))
		}
	}
	for _, name := range sortedKeys(d.InitialValues) {
		if _, ok := props[name]; !ok {
			add("/initial_values/"+name, "unknown_input", fmt.Sprintf("%q is not an input of the form's schema.", name))
		}
	}
	errs = append(errs, checkBlueprint(itemType, d.Blueprint, d.UiSchema.Fields, prefix+"/blueprint", publish)...)
	return errs
}

func checkValueSource(src api.ValueSource, ptr string) []api.FieldError {
	switch src.Kind {
	case api.Builtin:
		if src.Name == nil || !slices.Contains(builtinSources, *src.Name) {
			return []api.FieldError{fieldErr(ptr+"/name", "unknown", fmt.Sprintf("%q is not a built-in value source.", deref(src.Name)))}
		}
	case api.Rest, api.Cel:
		return checkCustomSource(src, ptr)
	default:
		return []api.FieldError{fieldErr(ptr+"/kind", "unknown", fmt.Sprintf("%q is not a kind of value source.", src.Kind))}
	}
	return nil
}

func checkBlueprint(itemType string, bp map[string]any, fields map[string]api.UiField, ptr string, publish bool) []api.FieldError {
	raw, _ := json.Marshal(bp)
	b, err := blueprint.Parse(raw)
	if err != nil {
		return []api.FieldError{fieldErr(ptr, "invalid", err.Error())}
	}
	want := itemKinds[itemType]
	if b.Kind != want {
		return []api.FieldError{fieldErr(ptr+"/kind", "invalid", fmt.Sprintf("A %s item needs a blueprint of kind %q.", itemType, want))}
	}
	var errs []api.FieldError
	switch b.Kind {
	case blueprint.KindVM:
		if b.Template == "" && b.Bindings["template"] == "" && publish {
			errs = append(errs, fieldErr(ptr+"/template", "required",
				"Name the vCenter template the VMs are cloned from, or take it from an input (bindings.template, for example inputs.template with rvav.templates)."))
		}
		for _, k := range sortedKeys(b.Bindings) {
			if err := policy.Check(b.Bindings[k]); err != nil {
				errs = append(errs, fieldErr(ptr+"/bindings/"+k, "invalid", "This binding is not valid CEL: "+err.Error()))
			}
		}
		for _, k := range sortedKeys(b.AWXTemplates) {
			if b.AWXTemplates[k].JobTemplate == "" && publish {
				errs = append(errs, fieldErr(ptr+"/awx_templates/"+k+"/job_template", "required", "Name the AWX template."))
			}
		}
	case blueprint.KindWorkflow:
		if b.Workflow == "" && publish {
			errs = append(errs, fieldErr(ptr+"/workflow", "required", "Name the registered custom workflow it starts."))
		}
	case blueprint.KindComposite:
		errs = append(errs, checkComposite(b, ptr, publish)...)
	case blueprint.KindAWX:
		if b.AWX == nil || b.AWX.JobTemplate == "" {
			if publish {
				errs = append(errs, fieldErr(ptr+"/awx/job_template", "required", "Name the AWX template it launches."))
			}
		} else if b.AWX.Workflow != (itemType == "awx_workflow") {
			errs = append(errs, fieldErr(ptr+"/awx/workflow", "invalid", "An awx_workflow item launches a workflow template, an awx_job_template item a job template."))
		}
		// Launch settings come from inputs picked from AWX.
		picked := func(name, source string) bool {
			f, ok := fields[name]
			return ok && f.ValueSource != nil && f.ValueSource.Kind == api.Builtin && deref(f.ValueSource.Name) == source
		}
		if b.AWX != nil && b.AWX.Inventory != "" && !picked(b.AWX.Inventory, "awx.inventories") {
			errs = append(errs, fieldErr(ptr+"/awx/inventory", "invalid", fmt.Sprintf("%q must be an input picked from awx.inventories.", b.AWX.Inventory)))
		}
		if b.AWX != nil {
			for i, c := range b.AWX.Credentials {
				if !picked(c, "awx.credentials") {
					errs = append(errs, fieldErr(fmt.Sprintf("%s/awx/credentials/%d", ptr, i), "invalid", fmt.Sprintf("%q must be an input picked from awx.credentials.", c)))
				}
			}
		}
	}
	return errs
}

func defaultIcon(itemType string) string {
	switch itemType {
	case "awx_job_template":
		return "terminal"
	case "awx_workflow":
		return "workflow"
	}
	return "server"
}

// starterDefinition is a new item's first draft: one text input, and a
// blueprint for its type to fill in.
func starterDefinition(itemType, name string) api.CatalogVersionDefinition {
	d := api.CatalogVersionDefinition{
		FormSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []any{"name"},
			"properties": map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 63}}},
		UiSchema:      api.UiSchema{Fields: map[string]api.UiField{"name": {Widget: api.UiWidgetText, Label: "Name"}}},
		InitialValues: map[string]any{},
	}
	d.UiSchema.Layout.Kind = api.Page
	d.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: name, Fields: []string{"name"}}}
	switch itemType {
	case "vm_blueprint":
		d.Blueprint = map[string]any{"kind": blueprint.KindVM, "template": "", "naming": "",
			"guest": map[string]any{"os_family": "linux", "domain": ""}}
	case "workflow":
		d.Blueprint = map[string]any{"kind": blueprint.KindWorkflow, "workflow": ""}
	case "composite":
		d.Blueprint = map[string]any{"kind": blueprint.KindComposite, "rollback_on_failure": true, "steps": []any{}}
	default:
		d.Blueprint = map[string]any{"kind": blueprint.KindAWX, "awx": map[string]any{"title": name, "job_template": "",
			"workflow": itemType == "awx_workflow"}}
	}
	return d
}

// diffJSON lists what changed from a to b, as JSON pointers under path.
// Lists of plain values say which values were added or removed; other
// lists that differ are one change.
func diffJSON(path string, a, b any) []api.CatalogVersionChange {
	ptr := path
	if ptr == "" {
		ptr = "/"
	}
	name := strings.TrimPrefix(path, "/")
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			break
		}
		var out []api.CatalogVersionChange
		keys := sortedKeys(av)
		for _, k := range sortedKeys(bv) {
			if _, in := av[k]; !in {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, k := range keys {
			sub := path + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(k)
			x, inA := av[k]
			y, inB := bv[k]
			switch {
			case !inA:
				out = append(out, api.CatalogVersionChange{Path: sub, Change: api.CatalogVersionChangeChangeAdded, To: &y,
					Summary: strings.TrimPrefix(sub, "/") + " added"})
			case !inB:
				out = append(out, api.CatalogVersionChange{Path: sub, Change: api.CatalogVersionChangeChangeRemoved, From: &x,
					Summary: strings.TrimPrefix(sub, "/") + " removed"})
			default:
				out = append(out, diffJSON(sub, x, y)...)
			}
		}
		return out
	case []any:
		bv, ok := b.([]any)
		if !ok || !scalars(av) || !scalars(bv) {
			break
		}
		var out []api.CatalogVersionChange
		for _, y := range bv {
			if !slices.ContainsFunc(av, func(x any) bool { return fmt.Sprint(x) == fmt.Sprint(y) }) {
				out = append(out, api.CatalogVersionChange{Path: ptr, Change: api.CatalogVersionChangeChangeAdded, To: &y, Summary: fmt.Sprintf("%s: %v added", name, y)})
			}
		}
		for _, x := range av {
			if !slices.ContainsFunc(bv, func(y any) bool { return fmt.Sprint(x) == fmt.Sprint(y) }) {
				out = append(out, api.CatalogVersionChange{Path: ptr, Change: api.CatalogVersionChangeChangeRemoved, From: &x, Summary: fmt.Sprintf("%s: %v removed", name, x)})
			}
		}
		if len(out) == 0 && fmt.Sprint(av) != fmt.Sprint(bv) {
			out = append(out, api.CatalogVersionChange{Path: ptr, Change: api.CatalogVersionChangeChangeChanged, From: &a, To: &b, Summary: name + ": order changed"})
		}
		return out
	}
	if equalJSON(a, b) {
		return nil
	}
	return []api.CatalogVersionChange{{Path: ptr, Change: api.CatalogVersionChangeChangeChanged, From: &a, To: &b,
		Summary: fmt.Sprintf("%s: %s → %s", name, short(a), short(b))}}
}

func scalars(list []any) bool {
	for _, v := range list {
		switch v.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func short(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}
