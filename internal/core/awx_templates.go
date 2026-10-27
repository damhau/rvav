package core

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
)

func canAdminCatalog(p *auth.Principal) bool { return p.IsAdmin() || p.HasGlobal("catalog-admin") }

// AWXTemplates lists the imported AWX templates with what changed in their
// surveys since their items' forms were made (ADR-0016).
func (s *Service) AWXTemplates(ctx context.Context, p *auth.Principal) (api.AwxTemplateList, error) {
	out := api.AwxTemplateList{Items: []api.AwxTemplate{}}
	if !canAdminCatalog(p) {
		return out, ErrForbidden
	}
	rows, err := s.Store.AWXTemplates(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		var cur, imported []awx.SurveyQuestion
		_ = json.Unmarshal(r.Survey, &cur)
		_ = json.Unmarshal(r.ImportedSurvey, &imported)
		diff := awxcatalog.Diff(imported, cur)
		out.Items = append(out.Items, api.AwxTemplate{TemplateId: r.AWXID, Name: r.Name, Workflow: r.Workflow,
			ItemId: r.ItemID, ItemName: r.ItemName, SurveyChanged: len(diff) > 0, SurveyDiff: append([]string{}, diff...),
			Warnings: append([]string{}, r.Warnings...), Gone: r.Gone, ModifiedAt: r.ModifiedAt, SyncedAt: r.SyncedAt})
	}
	return out, nil
}

// SyncAWXTemplates imports the AWX templates now.
func (s *Service) SyncAWXTemplates(ctx context.Context, p *auth.Principal) (api.AwxTemplateList, error) {
	if !canAdminCatalog(p) {
		return api.AwxTemplateList{Items: []api.AwxTemplate{}}, ErrForbidden
	}
	if s.AWXCatalog == nil {
		return api.AwxTemplateList{Items: []api.AwxTemplate{}}, &UpstreamError{System: "AWX", Err: errors.New("no AWX is configured")}
	}
	if _, err := s.AWXCatalog.SyncOnce(ctx); err != nil {
		return api.AwxTemplateList{Items: []api.AwxTemplate{}}, &UpstreamError{System: "AWX", Err: err}
	}
	return s.AWXTemplates(ctx, p)
}
