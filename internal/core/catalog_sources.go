package core

import (
	"fmt"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/policy"
)

// CatalogIcons are the icons an item may have; the web app draws the same
// set (components/common/misc.tsx).
var CatalogIcons = []string{"server", "monitor", "database", "key-round", "shield", "globe", "lock", "users", "terminal", "workflow"}

// checkCustomSource checks a `rest` or `cel` value source's parameters.
func checkCustomSource(src api.ValueSource, ptr string) []api.FieldError {
	params := map[string]any{}
	if src.Params != nil {
		params = *src.Params
	}
	str := func(k string) string { s, _ := params[k].(string); return s }
	var errs []api.FieldError
	switch src.Kind {
	case api.Rest:
		if str("url") == "" {
			errs = append(errs, fieldErr(ptr+"/params/url", "required", "Give the URL the options come from."))
		}
	case api.Cel:
		options, value := str("options"), str("value")
		if options == "" && value == "" {
			errs = append(errs, fieldErr(ptr+"/params", "required", "Give an `options` or a `value` expression."))
		}
		for k, expr := range map[string]string{"options": options, "value": value} {
			if expr == "" {
				continue
			}
			if err := policy.Check(expr); err != nil {
				errs = append(errs, fieldErr(ptr+"/params/"+k, "invalid", fmt.Sprintf("This expression is not valid CEL: %v", err)))
			}
		}
	}
	return errs
}
