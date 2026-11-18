package core

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/api"
)

// The schema each widget needs to work: what it reads and writes.
var widgetShapes = map[api.UiWidget]struct {
	types  []string
	format string
	items  string
	what   string
}{
	api.UiWidgetDate:     {types: []string{"string"}, format: "date", what: `type: string with format: date`},
	api.UiWidgetDatetime: {types: []string{"string"}, format: "date-time", what: `type: string with format: date-time`},
	api.UiWidgetTags:     {types: []string{"array"}, items: "string", what: "a list of strings (type: array, items: {type: string})"},
	api.UiWidgetKeyValue: {types: []string{"object"}, what: "type: object, with additionalProperties: {type: string}"},
	api.UiWidgetDatagrid: {types: []string{"array"}, items: "object", what: "a list of rows (type: array, items: {type: object, properties: ...})"},
	api.UiWidgetDualList: {types: []string{"array"}, items: "string", what: "a list of strings (type: array, items: {type: string})"},
	api.UiWidgetObject:   {types: []string{"object"}, what: "type: object, with properties"},
}

// columned are the widgets that edit properties through ui.columns: a
// table its rows' (under items), an object its own; and what to say when
// columns and properties don't match.
var columned = map[api.UiWidget]struct{ under, none, missing string }{
	api.UiWidgetDatagrid: {under: "/items", none: "Give the table its columns, one per property of its rows.",
		missing: "The rows have no property %q: add it to the schema's items."},
	api.UiWidgetObject: {none: "Give the object its sub-fields, one per property.",
		missing: "The object has no property %q: add it to its properties."},
}

// columnTypes is the cell type each table column widget edits.
var columnTypes = map[api.UiColumnWidget][]string{
	api.UiColumnWidgetText:   {"string"},
	api.UiColumnWidgetNumber: {"integer", "number"},
	api.UiColumnWidgetToggle: {"boolean"},
	api.UiColumnWidgetSelect: {"string"},
}

// checkFieldShape refuses a field whose schema its widget can't read or
// write, and a table or an object whose columns don't match the properties
// they edit.
func checkFieldShape(name string, f api.UiField, prop map[string]any, ptr string) []api.FieldError {
	var errs []api.FieldError
	at := ptr + "/ui_schema/fields/" + name
	schemaAt := ptr + "/form_schema/properties/" + name
	if shape, ok := widgetShapes[f.Widget]; ok && prop != nil {
		t, _ := prop["type"].(string)
		format, _ := prop["format"].(string)
		items, _ := prop["items"].(map[string]any)
		itemType, _ := items["type"].(string)
		if !slices.Contains(shape.types, t) || format != shape.format || shape.items != "" && itemType != shape.items {
			errs = append(errs, fieldErr(schemaAt, "widget_mismatch",
				fmt.Sprintf("The %s widget needs %s.", f.Widget, shape.what)))
		}
	}
	if own, ok := ownSources[f.Widget]; ok && f.ValueSource != nil && (f.ValueSource.Kind != api.Builtin || deref(f.ValueSource.Name) != own) {
		errs = append(errs, fieldErr(at+"/value_source", "widget_mismatch",
			fmt.Sprintf("A %s field always takes its choices from %s: leave its value source out, or name that one.", f.Widget, own)))
	}
	how, ok := columned[f.Widget]
	if f.Columns != nil && !ok {
		errs = append(errs, fieldErr(at+"/columns", "invalid", "Only a table (datagrid) or an object has columns."))
	}
	if !ok || len(errs) > 0 || prop == nil {
		return errs
	}
	holder := prop
	if how.under != "" {
		holder, _ = prop["items"].(map[string]any)
	}
	cells, _ := holder["properties"].(map[string]any)
	cols := columnsOf(f)
	if len(cols) == 0 {
		return append(errs, fieldErr(at+"/columns", "required", how.none))
	}
	seen := map[string]bool{}
	for i, c := range cols {
		cat := fmt.Sprintf("%s/columns/%d", at, i)
		cell, _ := cells[c.Key].(map[string]any)
		switch {
		case seen[c.Key]:
			errs = append(errs, fieldErr(cat+"/key", "duplicate", fmt.Sprintf("Another column already edits %q.", c.Key)))
		case cell == nil:
			errs = append(errs, fieldErr(cat+"/key", "unknown_input", fmt.Sprintf(how.missing, c.Key)))
		case strings.TrimSpace(c.Label) == "":
			errs = append(errs, fieldErr(cat+"/label", "required", "Give the column a label."))
		case !slices.Contains(columnTypes[c.Widget], fmt.Sprint(cell["type"])):
			errs = append(errs, fieldErr(cat+"/widget", "widget_mismatch",
				fmt.Sprintf("A %s column edits %s, and %q is %v.", c.Widget, strings.Join(columnTypes[c.Widget], " or "), c.Key, cell["type"])))
		case c.Widget == api.UiColumnWidgetSelect:
			enum, _ := cell["enum"].([]any)
			if len(enum) == 0 {
				errs = append(errs, fieldErr(cat+"/options", "required",
					fmt.Sprintf("A select column's property needs its choices as an enum, so the server checks them: %q has none.", c.Key)))
			}
			for j, o := range optionsOf(c.Options) {
				if !slices.Contains(enum, any(o.Value)) {
					errs = append(errs, fieldErr(fmt.Sprintf("%s/options/%d", cat, j), "enum",
						fmt.Sprintf("%q isn't in the enum of %q.", o.Value, c.Key)))
				}
			}
		}
		seen[c.Key] = true
	}
	for _, k := range sortedKeys(cells) {
		if !seen[k] {
			errs = append(errs, fieldErr(schemaAt+how.under+"/properties/"+k, "no_ui_field",
				fmt.Sprintf("%q has no column, so nobody could fill it in.", k)))
		}
	}
	return errs
}

// shownOnly are the fields that show something and take no input.
var shownOnly = map[api.UiWidget]bool{api.UiWidgetInfo: true, api.UiWidgetImage: true, api.UiWidgetLink: true}

// checkShown checks what an image or a link shows. An address must be
// http(s) or a path on rvav's own site whatever the draft, so a saved form
// can't run a script (javascript:) or embed a document (data:); to
// publish, the address and an image's text alternative must be given.
func checkShown(name string, f api.UiField, input bool, ptr string, publish bool) []api.FieldError {
	var errs []api.FieldError
	at := ptr + "/ui_schema/fields/" + name
	media := f.Widget == api.UiWidgetImage || f.Widget == api.UiWidgetLink
	if f.Url != nil && !media {
		errs = append(errs, fieldErr(at+"/url", "invalid", "Only an image or a link has an address (url)."))
	}
	if f.Alt != nil && f.Widget != api.UiWidgetImage {
		errs = append(errs, fieldErr(at+"/alt", "invalid", "Only an image has a text alternative (alt)."))
	}
	if !media {
		return errs
	}
	what := map[api.UiWidget]string{api.UiWidgetImage: "An image", api.UiWidgetLink: "A link"}[f.Widget]
	if input {
		errs = append(errs, fieldErr(ptr+"/form_schema/properties/"+name, "widget_mismatch",
			fmt.Sprintf("%s takes no input: remove %q from the form's schema.", what, name)))
	}
	switch u := strings.TrimSpace(deref(f.Url)); {
	case u == "" && publish:
		errs = append(errs, fieldErr(at+"/url", "required", fmt.Sprintf("%s needs its address (url).", what)))
	case u != "" && !shownURL(u):
		errs = append(errs, fieldErr(at+"/url", "invalid",
			"Give an address that starts with https:// or http://, or a path on this site that starts with /."))
	}
	if f.Widget == api.UiWidgetImage && strings.TrimSpace(deref(f.Alt)) == "" && publish {
		errs = append(errs, fieldErr(at+"/alt", "required",
			"Describe the image for those who can't see it: give its text alternative (alt)."))
	}
	return errs
}

// shownURL is an address a form may show or link to: absolute http(s), or
// a path on this site. Browsers read "//host" and "/\host" as another
// site's, so neither is a path.
func shownURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme == "" {
		return u.Host == "" && strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.Contains(s, `\`)
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// locateNested says where in a table, a list, an object or a set of pairs
// a problem is, since the form shows one message per field: "Row 2, Size:
// Enter 10 or more."
func locateNested(ui api.UiSchema, errs []api.FieldError) []api.FieldError {
	for i, e := range errs {
		parts := strings.Split(strings.TrimPrefix(e.Field, "/inputs/"), "/")
		if !strings.HasPrefix(e.Field, "/inputs/") || len(parts) < 2 {
			continue
		}
		f, ok := ui.Fields[parts[0]]
		if !ok {
			continue
		}
		var where string
		n, isIndex := strconv.Atoi(parts[1])
		switch {
		case f.Widget == api.UiWidgetKeyValue:
			where = strings.NewReplacer("~1", "/", "~0", "~").Replace(parts[1])
		case f.Widget == api.UiWidgetObject:
			where = columnLabel(f, parts[1])
		case isIndex != nil:
			continue
		case f.Widget == api.UiWidgetDatagrid:
			where = "Row " + strconv.Itoa(n+1)
			if len(parts) > 2 {
				where += ", " + columnLabel(f, parts[2])
			}
		default:
			where = "Item " + strconv.Itoa(n+1)
		}
		errs[i].Message = where + ": " + e.Message
	}
	return errs
}

// columnLabel is the label of the column editing key, else the key.
func columnLabel(f api.UiField, key string) string {
	if j := slices.IndexFunc(columnsOf(f), func(c api.UiColumn) bool { return c.Key == key }); j >= 0 {
		return columnsOf(f)[j].Label
	}
	return key
}

func columnsOf(f api.UiField) []api.UiColumn {
	if f.Columns == nil {
		return nil
	}
	return *f.Columns
}

func optionsOf(o *[]api.Option) []api.Option {
	if o == nil {
		return nil
	}
	return *o
}
