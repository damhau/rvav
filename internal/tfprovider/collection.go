package tfprovider

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// collection is one of the API's lists of objects with IDs: POST to make
// one, PUT or PATCH to change it, DELETE to remove it. Without get, an
// object is read from the list.
type collection struct {
	list   func(ctx context.Context, c *conn) (*http.Response, error)
	get    func(ctx context.Context, c *conn, id string) (*http.Response, error)
	create func(ctx context.Context, c *conn, body io.Reader) (*http.Response, error)
	update func(ctx context.Context, c *conn, id string, body io.Reader) (*http.Response, error)
	del    func(ctx context.Context, c *conn, id string) (*http.Response, error)
	// present turns an object of the API into the resource's, when their
	// fields differ.
	present func(obj map[string]any) map[string]any
}

var errNoUpdate = errors.New("rvav can't change it: Terraform replaces it")

func (k collection) resource(name string, s schema.Schema) *restResource {
	show := func(obj map[string]any, err error) (map[string]any, error) {
		if obj == nil || err != nil || k.present == nil {
			return obj, err
		}
		return k.present(obj), nil
	}
	r := &restResource{name: name, schema: s}
	r.create = func(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
		return show(object(k.create(ctx, c, send(body))))
	}
	r.read = func(ctx context.Context, c *conn, id string, _ map[string]any) (map[string]any, error) {
		if k.get != nil {
			return show(gone(object(k.get(ctx, c, id))))
		}
		list, err := object(k.list(ctx, c))
		return show(find(list, err, "id", id))
	}
	r.update = func(ctx context.Context, c *conn, id string, body, _ map[string]any) (map[string]any, error) {
		if k.update == nil {
			return nil, errNoUpdate
		}
		return show(object(k.update(ctx, c, id, send(body))))
	}
	r.del = func(ctx context.Context, c *conn, id string, _ map[string]any) error {
		return done(k.del(ctx, c, id))
	}
	return r
}

func required(desc string) schema.StringAttribute {
	return schema.StringAttribute{Required: true, MarkdownDescription: desc}
}

func optional(desc string) schema.StringAttribute {
	return schema.StringAttribute{Optional: true, MarkdownDescription: desc}
}

func stringSet(desc string) schema.SetAttribute {
	return schema.SetAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: desc}
}

func number(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{Optional: true, MarkdownDescription: desc}
}

func priority() schema.Int64Attribute {
	return schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(100),
		MarkdownDescription: "Lower goes first. Default: `100`."}
}

func enabled() schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
		MarkdownDescription: "Applies when true. Default: `true`."}
}

func enforcement() schema.StringAttribute {
	return schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("hard"),
		MarkdownDescription: "`hard` refuses; `soft` warns and lets the request through. Default: `hard`."}
}

func projectScope() schema.StringAttribute {
	return optional("The project it applies in, by ID. Default: every project.")
}

func environmentScope() schema.StringAttribute {
	return optional("The environment it applies in, by key. Default: every environment.")
}

func condition(desc string) schema.StringAttribute {
	return required("A CEL expression " + desc + ".")
}
