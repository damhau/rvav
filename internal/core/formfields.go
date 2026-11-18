package core

import (
	"fmt"
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
}

// columnTypes is the cell type each table column widget edits.
var columnTypes = map[api.UiColumnWidget][]string{
	api.UiColumnWidgetText:   {"string"},
	api.UiColumnWidgetNumber: {"integer", "number"},
	api.UiColumnWidgetToggle: {"boolean"},
	api.UiColumnWidgetSelect: {"string"},
}

// checkFieldShape refuses a field whose schema its widget can't read or
// write, and a table whose columns don't match its rows' properties.
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
	if f.Columns != nil && f.Widget != api.UiWidgetDatagrid {
		errs = append(errs, fieldErr(at+"/columns", "invalid", "Only a table (datagrid) has columns."))
	}
	if f.Widget != api.UiWidgetDatagrid || len(errs) > 0 || prop == nil {
		return errs
	}
	items, _ := prop["items"].(map[string]any)
	cells, _ := items["properties"].(map[string]any)
	cols := columnsOf(f)
	if len(cols) == 0 {
		return append(errs, fieldErr(at+"/columns", "required", "Give the table its columns, one per property of its rows."))
	}
	seen := map[string]bool{}
	for i, c := range cols {
		cat := fmt.Sprintf("%s/columns/%d", at, i)
		cell, _ := cells[c.Key].(map[string]any)
		switch {
		case seen[c.Key]:
			errs = append(errs, fieldErr(cat+"/key", "duplicate", fmt.Sprintf("Another column already edits %q.", c.Key)))
		case cell == nil:
			errs = append(errs, fieldErr(cat+"/key", "unknown_input",
				fmt.Sprintf("The rows have no property %q: add it to the schema's items.", c.Key)))
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
			errs = append(errs, fieldErr(schemaAt+"/items/properties/"+k, "no_ui_field",
				fmt.Sprintf("%q has no column, so nobody could fill it in.", k)))
		}
	}
	return errs
}

// locateNested says where in a table, a list or a set of pairs a problem
// is, since the form shows one message per field: "Row 2, Size: Enter 10
// or more."
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
		case isIndex != nil:
			continue
		case f.Widget == api.UiWidgetDatagrid:
			where = "Row " + strconv.Itoa(n+1)
			if len(parts) > 2 {
				label := parts[2]
				if j := slices.IndexFunc(columnsOf(f), func(c api.UiColumn) bool { return c.Key == parts[2] }); j >= 0 {
					label = columnsOf(f)[j].Label
				}
				where += ", " + label
			}
		default:
			where = "Item " + strconv.Itoa(n+1)
		}
		errs[i].Message = where + ": " + e.Message
	}
	return errs
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
