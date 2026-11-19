package tfprovider

import (
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func pathOf(name string) path.Path { return path.Root(name) }

// readData sets a data source's state to rvav's object.
func readData(state *tfsdk.State, diags *diag.Diagnostics, obj map[string]any) {
	attrs := map[string]dschema.Attribute{}
	for name, a := range state.Schema.GetAttributes() {
		if da, ok := a.(dschema.Attribute); ok {
			attrs[name] = da
		}
	}
	f := dataFields(attrs)
	v, err := fromJSON(f, obj, tftypes.NewValue(f.typ, tftypes.UnknownValue))
	if err != nil {
		diags.AddError("Unexpected answer from rvav", err.Error())
		return
	}
	state.Raw = v
}
