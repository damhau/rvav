package core

import (
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// Every action has a category the API lists, by its first word.
func TestAuditCategory(t *testing.T) {
	for action, want := range map[string]api.AuditCategory{
		"auth.sign_in": api.AuditCategorySignIn, "auth.sign_in_failed": api.AuditCategorySignIn, "token.create": api.AuditCategoryToken,
		"awx.inventory_token": api.AuditCategoryToken, "awx.inventories": api.AuditCategorySettings,
		"member.set": api.AuditCategoryAccess, "group_mapping.create": api.AuditCategoryAccess,
		"catalog.version.publish": api.AuditCategoryCatalog, "naming.update": api.AuditCategoryPolicy,
		"vcenter.update": api.AuditCategoryInfrastructure, "subscription.test": api.AuditCategoryExtensibility,
		"settings.audit_destination.create": api.AuditCategorySettings, "wf.decommission": api.AuditCategoryRequest,
		"approval.decide": api.AuditCategoryRequest, "something.new": api.AuditCategoryOther, "": api.AuditCategoryOther,
	} {
		got := api.AuditCategory(store.AuditCategory(action))
		if got != want || !got.Valid() {
			t.Errorf("%q: %s, want %s", action, got, want)
		}
	}
}
