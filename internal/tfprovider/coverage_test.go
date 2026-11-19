package tfprovider

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/damhau/rvav/internal/api"
)

// TestResourcesCoverWriteSchemas fails when the API's write schema of a
// resource gains a field the resource doesn't have: an apply would send
// the object without it, and clear what the web app set.
func TestResourcesCoverWriteSchemas(t *testing.T) {
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]*openapi3.Operation{}
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			// The embedded spec's operation IDs are Go-cased.
			ops[strings.ToLower(op.OperationID)] = op
		}
	}
	for _, c := range []struct {
		resource   func() resource.Resource
		operations []string
		// left are the bodies' fields the resource doesn't manage, and why.
		left map[string]string
	}{
		{newProjectResource, []string{"createProject", "updateProject"},
			map[string]string{"project_admins": "members are rvav_project_member resources",
				"archived": "destroying archives a project, and creating it brings it back"}},
		{newApprovalPolicyResource, []string{"createApprovalPolicy", "updateApprovalPolicy"}, nil},
		{newDay2PolicyResource, []string{"createDay2Policy", "updateDay2Policy"}, nil},
		{newNamingPolicyResource, []string{"createNamingPolicy", "updateNamingPolicy"}, nil},
		{newLeasePolicyResource, []string{"setLeasePolicy"}, nil},
		{newLeaseRuleResource, []string{"createLeaseRule", "updateLeaseRule"}, nil},
		{newDeploymentLimitPolicyResource, []string{"createDeploymentLimitPolicy", "updateDeploymentLimitPolicy"}, nil},
		{newRoleResource, []string{"createRole", "updateRole"}, nil},
		{newRoleAssignmentResource, []string{"createRoleAssignment"}, nil},
		{newGroupMappingResource, []string{"createGroupMapping"}, nil},
		{newSecretResource, []string{"createSecret", "updateSecret"}, nil},
		{newSubscriptionResource, []string{"createSubscription", "updateSubscription"}, nil},
	} {
		var s resource.SchemaResponse
		c.resource().Schema(context.Background(), resource.SchemaRequest{}, &s)
		var m resource.MetadataResponse
		c.resource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rvav"}, &m)
		for _, name := range c.operations {
			op := ops[strings.ToLower(name)]
			if op == nil || op.RequestBody == nil {
				t.Errorf("%s: no operation %s with a body", m.TypeName, name)
				continue
			}
			body := op.RequestBody.Value.Content.Get("application/json").Schema.Value
			for _, field := range properties(body) {
				if _, ok := s.Schema.Attributes[field]; !ok && c.left[field] == "" {
					t.Errorf("%s has no attribute %s, which %s takes", m.TypeName, field, name)
				}
			}
		}
	}
}

func properties(s *openapi3.Schema) []string {
	var out []string
	for name, p := range s.Properties {
		if p.Value == nil || !p.Value.ReadOnly {
			out = append(out, name)
		}
	}
	for _, part := range s.AllOf {
		out = append(out, properties(part.Value)...)
	}
	sort.Strings(out)
	return out
}
