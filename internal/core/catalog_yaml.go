package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// Entitlements, categories, icons, and items as YAML (ADR-0019).

// CatalogItemEntitlements are the projects that may request an item.
func (s *Service) CatalogItemEntitlements(ctx context.Context, p *auth.Principal, idOrSlug string) (api.CatalogEntitlements, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	return s.entitlements(ctx, it)
}

// SetCatalogItemEntitlements opens an item to every project, or to chosen
// projects.
func (s *Service) SetCatalogItemEntitlements(ctx context.Context, p *auth.Principal, idOrSlug string, e api.CatalogEntitlements) (api.CatalogEntitlements, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	unknown, err := s.Store.UnknownProjects(ctx, e.ProjectIds)
	if err != nil {
		return api.CatalogEntitlements{}, err
	}
	if len(unknown) > 0 {
		return api.CatalogEntitlements{}, &ValidationError{Errors: []api.FieldError{fieldErr("/project_ids", "unknown",
			"No such project: "+strings.Join(unknown, ", ")+".")}}
	}
	projects := slices.Compact(slices.Sorted(slices.Values(e.ProjectIds)))
	if err := s.Store.SetEntitlements(ctx, it.ID, e.AllProjects, projects); err != nil {
		return api.CatalogEntitlements{}, err
	}
	s.auditCatalog(ctx, p, "catalog.item.entitlements", it.ID, map[string]any{"all_projects": e.AllProjects, "projects": projects})
	return s.CatalogItemEntitlements(ctx, p, it.ID)
}

var categoryKey = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

func presentCategory(c store.AdminCategory) api.AdminCatalogCategory {
	return api.AdminCatalogCategory{Key: c.Key, Name: c.Name, SortOrder: &c.SortOrder, Items: &c.Items}
}

// AdminCatalogCategories lists every category with its number of items.
func (s *Service) AdminCatalogCategories(ctx context.Context, p *auth.Principal) (api.AdminCatalogCategoryList, error) {
	out := api.AdminCatalogCategoryList{Items: []api.AdminCatalogCategory{}}
	if !canAdminCatalog(p) {
		return out, ErrForbidden
	}
	list, err := s.Store.AdminCategories(ctx)
	for _, c := range list {
		out.Items = append(out.Items, presentCategory(c))
	}
	return out, err
}

func (s *Service) adminCategory(ctx context.Context, key string) (api.AdminCatalogCategory, error) {
	list, err := s.Store.AdminCategories(ctx)
	if err != nil {
		return api.AdminCatalogCategory{}, err
	}
	for _, c := range list {
		if c.Key == key {
			return presentCategory(c), nil
		}
	}
	return api.AdminCatalogCategory{}, ErrNotFound
}

// CreateCatalogCategory adds a category.
func (s *Service) CreateCatalogCategory(ctx context.Context, p *auth.Principal, c api.AdminCatalogCategory) (api.AdminCatalogCategory, error) {
	if !canAdminCatalog(p) {
		return api.AdminCatalogCategory{}, ErrForbidden
	}
	var errs []api.FieldError
	if !categoryKey.MatchString(c.Key) {
		errs = append(errs, fieldErr("/key", "pattern", "Lower case letters, digits and dashes, starting with a letter, 2 to 31 characters."))
	}
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 60 {
		errs = append(errs, fieldErr("/name", "required", "Give the category a name of 1 to 60 characters."))
	}
	if len(errs) > 0 {
		return api.AdminCatalogCategory{}, &ValidationError{Errors: errs}
	}
	row := store.AdminCategory{Key: c.Key, Name: strings.TrimSpace(c.Name)}
	if c.SortOrder != nil {
		row.SortOrder = *c.SortOrder
	}
	if err := s.Store.CreateCategory(ctx, row); errors.Is(err, store.ErrInUse) {
		return api.AdminCatalogCategory{}, &ConflictError{Msg: fmt.Sprintf("The category %q exists.", c.Key)}
	} else if err != nil {
		return api.AdminCatalogCategory{}, err
	}
	s.auditCatalog(ctx, p, "catalog.category.create", c.Key, map[string]any{"name": row.Name})
	return s.adminCategory(ctx, c.Key)
}

// UpdateCatalogCategory renames or reorders a category.
func (s *Service) UpdateCatalogCategory(ctx context.Context, p *auth.Principal, key string, u api.AdminCatalogCategoryUpdate) (api.AdminCatalogCategory, error) {
	if !canAdminCatalog(p) {
		return api.AdminCatalogCategory{}, ErrForbidden
	}
	if u.Name != nil && (strings.TrimSpace(*u.Name) == "" || len(*u.Name) > 60) {
		return api.AdminCatalogCategory{}, &ValidationError{Errors: []api.FieldError{fieldErr("/name", "required", "Give the category a name of 1 to 60 characters.")}}
	}
	if err := s.Store.UpdateCategory(ctx, key, u.Name, u.SortOrder); errors.Is(err, store.ErrNotFound) {
		return api.AdminCatalogCategory{}, ErrNotFound
	} else if err != nil {
		return api.AdminCatalogCategory{}, err
	}
	s.auditCatalog(ctx, p, "catalog.category.update", key, map[string]any{"changes": u})
	return s.adminCategory(ctx, key)
}

// DeleteCatalogCategory removes an empty category.
func (s *Service) DeleteCatalogCategory(ctx context.Context, p *auth.Principal, key string) error {
	if !canAdminCatalog(p) {
		return ErrForbidden
	}
	switch err := s.Store.DeleteCategory(ctx, key); {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrInUse):
		return &ConflictError{Msg: "Items are in this category: move them to another one first."}
	case err != nil:
		return err
	}
	s.auditCatalog(ctx, p, "catalog.category.delete", key, nil)
	return nil
}

// itemDoc is an item as YAML.
type itemDoc struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   itemMetadata `yaml:"metadata"`
	Versions   []versionDoc `yaml:"versions"`
}

type itemMetadata struct {
	Slug        string   `yaml:"slug"`
	Name        string   `yaml:"name"`
	Summary     string   `yaml:"summary"`
	Description string   `yaml:"description,omitempty"`
	Category    string   `yaml:"category"`
	Type        string   `yaml:"type"`
	Icon        string   `yaml:"icon"`
	Tags        []string `yaml:"tags,omitempty"`
	Featured    bool     `yaml:"featured,omitempty"`
}

type versionDoc struct {
	Number        string         `yaml:"number"`
	State         string         `yaml:"state,omitempty"`
	Changelog     string         `yaml:"changelog,omitempty"`
	FormSchema    map[string]any `yaml:"form_schema"`
	UISchema      map[string]any `yaml:"ui_schema"`
	InitialValues map[string]any `yaml:"initial_values,omitempty"`
	Blueprint     map[string]any `yaml:"blueprint"`
}

const docAPIVersion, docKind = "rvav.dev/v1", "CatalogItem"

// ExportCatalogItem is the item as YAML, with one version or all of them
// (oldest first).
func (s *Service) ExportCatalogItem(ctx context.Context, p *auth.Principal, idOrSlug, versionID string) ([]byte, string, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return nil, "", err
	}
	out, err := s.itemYAML(ctx, it, func(row store.VersionRow) bool { return versionID == "" || row.ID == versionID })
	if err != nil {
		return nil, "", err
	}
	return out, it.Slug + ".yaml", nil
}

// itemYAML is the item as YAML, with the versions keep keeps (oldest
// first); ErrNotFound when it keeps none.
func (s *Service) itemYAML(ctx context.Context, it store.AdminItem, keep func(store.VersionRow) bool) ([]byte, error) {
	doc := itemDoc{APIVersion: docAPIVersion, Kind: docKind, Metadata: itemMetadata{Slug: it.Slug, Name: it.Name,
		Summary: it.Summary, Description: it.DescriptionMD, Category: it.Category, Type: it.Type, Icon: it.Icon, Tags: it.Tags,
		Featured: it.Featured}}
	rows, err := s.Store.ItemVersions(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	slices.Reverse(rows)
	for _, row := range rows {
		if !keep(row) {
			continue
		}
		v, err := s.Store.GetVersion(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		vd := versionDoc{Number: row.Number, State: row.State, Changelog: row.Changelog}
		for _, part := range []struct {
			raw json.RawMessage
			to  *map[string]any
		}{{v.FormSchema, &vd.FormSchema}, {v.UISchema, &vd.UISchema}, {v.InitialValues, &vd.InitialValues}, {v.Blueprint, &vd.Blueprint}} {
			if err := json.Unmarshal(part.raw, part.to); err != nil {
				return nil, err
			}
		}
		if len(vd.InitialValues) == 0 {
			vd.InitialValues = nil
		}
		doc.Versions = append(doc.Versions, vd)
	}
	if len(doc.Versions) == 0 {
		return nil, ErrNotFound
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ImportCatalogItem makes an item from YAML, or a new draft of one.
func (s *Service) ImportCatalogItem(ctx context.Context, p *auth.Principal, body []byte, itemID string) (api.CatalogImportResult, error) {
	if !canAdminCatalog(p) {
		return api.CatalogImportResult{}, ErrForbidden
	}
	var root yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		return api.CatalogImportResult{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "yaml", "This is not valid YAML: "+err.Error())}}
	}
	var doc itemDoc
	if err := root.Decode(&doc); err != nil {
		return api.CatalogImportResult{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "yaml", "This is not an rvav item: "+err.Error())}}
	}
	var errs []api.FieldError
	add := func(ptr, code, msg string) { errs = append(errs, fieldErr(ptr, code, msg)) }
	if doc.APIVersion != docAPIVersion || doc.Kind != docKind {
		add("/kind", "invalid", fmt.Sprintf("Expected apiVersion %s and kind %s.", docAPIVersion, docKind))
	}
	if len(doc.Versions) == 0 {
		add("/versions", "required", "The item has no version.")
	}
	defs := make([]api.CatalogVersionDefinition, len(doc.Versions))
	numbers := map[string]bool{}
	for i, v := range doc.Versions {
		ptr := "/versions/" + strconv.Itoa(i)
		if strings.TrimSpace(v.Number) == "" {
			add(ptr+"/number", "required", "Give the version a number.")
		} else if numbers[v.Number] {
			add(ptr+"/number", "duplicate", fmt.Sprintf("Version %s is in the file twice.", v.Number))
		}
		numbers[v.Number] = true
		raw, _ := json.Marshal(map[string]any{"form_schema": v.FormSchema, "ui_schema": v.UISchema,
			"initial_values": orEmpty(v.InitialValues), "blueprint": orEmpty(v.Blueprint)})
		if err := json.Unmarshal(raw, &defs[i]); err != nil {
			add(ptr, "invalid", "This version's definition doesn't fit: "+err.Error())
			continue
		}
	}

	var it store.AdminItem
	if itemID != "" {
		var err error
		if it, err = s.adminItem(ctx, p, itemID); err != nil {
			return api.CatalogImportResult{}, err
		}
		if doc.Metadata.Type != "" && doc.Metadata.Type != it.Type {
			add("/metadata/type", "invalid", fmt.Sprintf("The file is a %s item, %s is a %s.", doc.Metadata.Type, it.Slug, it.Type))
		}
	} else {
		m := doc.Metadata
		if !slugPattern.MatchString(m.Slug) {
			add("/metadata/slug", "pattern", "Lower case letters, digits and dashes, 2 to 63 characters.")
		}
		if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Summary) == "" {
			add("/metadata", "required", "The item needs a name and a summary.")
		}
		if _, ok := itemKinds[m.Type]; !ok {
			add("/metadata/type", "invalid", fmt.Sprintf("%q is not a type of item that can be imported.", m.Type))
		}
		if ok, err := s.Store.CategoryExists(ctx, m.Category); err != nil {
			return api.CatalogImportResult{}, err
		} else if !ok {
			add("/metadata/category", "unknown", fmt.Sprintf("There is no category %q.", m.Category))
		}
		if m.Icon == "" {
			doc.Metadata.Icon = defaultIcon(m.Type)
		} else if !slices.Contains(CatalogIcons, m.Icon) {
			add("/metadata/icon", "unknown", fmt.Sprintf("%q is not one of rvav's icons.", m.Icon))
		}
		it.Type = m.Type
	}
	if len(errs) == 0 {
		for i, d := range defs {
			errs = append(errs, checkDefinition(it.Type, d, "/versions/"+strconv.Itoa(i), false)...)
			refs, err := s.checkSourceRefs(ctx, d, "/versions/"+strconv.Itoa(i))
			if err != nil {
				return api.CatalogImportResult{}, err
			}
			errs = append(errs, refs...)
		}
	}
	if len(errs) > 0 {
		for i := range errs {
			if line := lineOf(&root, errs[i].Field); line > 0 {
				errs[i].Message += fmt.Sprintf(" (line %d)", line)
			}
		}
		return api.CatalogImportResult{}, &ValidationError{Errors: errs}
	}

	out := api.CatalogImportResult{Versions: []api.CatalogItemVersion{}}
	if itemID != "" {
		// The file's last version becomes the item's draft.
		last := len(doc.Versions) - 1
		existing, err := s.Store.ItemVersions(ctx, it.ID)
		if err != nil {
			return out, err
		}
		number := doc.Versions[last].Number
		if slices.ContainsFunc(existing, func(v store.VersionRow) bool { return v.Number == number }) {
			number = nextNumber(existing)
		}
		v := store.NewVersion{ID: ids.New(ids.Version), ItemID: it.ID, Number: number, Changelog: doc.Versions[last].Changelog,
			CreatedBy: p.User.ID, Def: storeDef(defs[last])}
		if err := s.Store.CreateDraft(ctx, v); errors.Is(err, store.ErrDraftExists) {
			return out, &ConflictError{Msg: it.Name + " already has a draft: discard it first, or change it."}
		} else if err != nil {
			return out, err
		}
		s.auditCatalog(ctx, p, "catalog.item.import", it.ID, map[string]any{"version": number})
		out.ItemId = it.ID
	} else {
		m := doc.Metadata
		item := store.NewItem{ID: ids.New(ids.Item), Slug: m.Slug, Name: strings.TrimSpace(m.Name), Summary: strings.TrimSpace(m.Summary),
			DescriptionMD: m.Description, Category: m.Category, Type: m.Type, Icon: m.Icon, Tags: m.Tags, Featured: m.Featured}
		var versions []store.ImportVersion
		for i, v := range doc.Versions {
			state := "deprecated"
			if i == len(doc.Versions)-1 {
				state = "draft" // nothing is published by an import
			}
			versions = append(versions, store.ImportVersion{State: state, NewVersion: store.NewVersion{ID: ids.New(ids.Version),
				Number: v.Number, Changelog: v.Changelog, CreatedBy: p.User.ID, Def: storeDef(defs[i])}})
		}
		if err := s.Store.ImportItem(ctx, item, versions); errors.Is(err, store.ErrInUse) {
			return out, &ConflictError{Msg: fmt.Sprintf("The slug %q is taken: import it into that item, or change the slug in the file.", m.Slug)}
		} else if err != nil {
			return out, err
		}
		s.auditCatalog(ctx, p, "catalog.item.import", item.ID, map[string]any{"slug": item.Slug, "versions": len(versions)})
		out.ItemId, out.Created = item.ID, true
	}
	rows, err := s.Store.ItemVersions(ctx, out.ItemId)
	for _, r := range rows {
		out.Versions = append(out.Versions, presentVersion(r))
	}
	return out, err
}

// lineOf finds the line of the YAML node a JSON pointer names (or of the
// deepest node on its way).
func lineOf(root *yaml.Node, pointer string) int {
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	line := n.Line
	for _, seg := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		seg = strings.NewReplacer("~1", "/", "~0", "~").Replace(seg)
		var next *yaml.Node
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == seg {
					next = n.Content[i+1]
					line = n.Content[i].Line
				}
			}
		case yaml.SequenceNode:
			if i, err := strconv.Atoi(seg); err == nil && i < len(n.Content) {
				next = n.Content[i]
				line = next.Line
			}
		}
		if next == nil {
			return line
		}
		n = next
	}
	return line
}
