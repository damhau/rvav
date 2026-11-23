package core

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/snowsync"
)

// ServiceNow (ADR-0065): the instance rvav keeps CIs in, and takes requests
// from.

// ServiceNowClient is a client of the instance the settings name.
func ServiceNowClient(c config.ServiceNowConfig, roots *x509.CertPool) (*servicenow.Client, error) {
	o := c.TLS()
	o.Roots = roots
	return servicenow.New(servicenow.Config{URL: c.URL, Auth: c.Auth, Username: c.Username, ClientID: c.ClientID, Secret: c.Secret, TLS: o})
}

// ServiceNowCmdb is what rvav keeps in ServiceNow's CMDB, and what the
// last reconcile found.
func (s *Service) ServiceNowCmdb(ctx context.Context, p *auth.Principal) (api.ServiceNowCmdb, error) {
	if !p.Everywhere(auth.IntegrationsManage) {
		return api.ServiceNowCmdb{}, ErrForbidden
	}
	c := s.liveConfig(ctx).ServiceNow
	out := api.ServiceNowCmdb{Enabled: c.Configured() && c.CMDB, Class: c.Class, DataSource: c.DataSource, Projects: []api.ProjectRef{},
		Failures: []api.ServiceNowFailure{}, Orphans: []api.ServiceNowOrphan{}}
	for _, code := range c.Projects {
		if pr, err := s.Store.GetProject(ctx, code); err == nil {
			out.Projects = append(out.Projects, api.ProjectRef{Id: pr.ID, Code: pr.Code, Name: pr.Name})
		}
	}
	n, err := s.Store.ServiceNowCounts(ctx)
	if err != nil {
		return out, err
	}
	out.Kept, out.Retired, out.Queued, out.Failing = n.Kept, n.Retired, n.Queued, n.Failing
	failures, err := s.Store.ServiceNowFailures(ctx, 50)
	if err != nil {
		return out, err
	}
	for _, f := range failures {
		out.Failures = append(out.Failures, api.ServiceNowFailure{DeploymentId: f.DeploymentID, DeploymentName: f.DeploymentName,
			Attempts: f.Attempts, Error: f.LastError, NextAttemptAt: f.DueAt})
	}
	st, err := s.Store.ServiceNowState(ctx)
	if err != nil {
		return out, err
	}
	var r snowsync.Report
	if st.ReconciledAt != nil && json.Unmarshal(st.Report, &r) == nil {
		out.ReconciledAt = st.ReconciledAt
		out.NextReconcileAt = new(st.ReconciledAt.Add(c.SyncInterval))
		out.OrphansTotal = r.Orphaned
		if r.Error != "" {
			out.ReconcileError = new(r.Error)
		} else {
			out.Servicenow = new(r.ServiceNow)
		}
		for _, o := range r.Orphans {
			out.Orphans = append(out.Orphans, api.ServiceNowOrphan{SysId: o.SysID, Name: o.Name, InstanceUuid: nonEmpty(o.InstanceUUID),
				UpdatedAt: nonEmpty(o.UpdatedAt), Url: servicenow.RecordURL(c.URL, c.Class, o.SysID)})
		}
	}
	return out, nil
}

// ReconcileServiceNowCmdb has the next pass reconcile.
func (s *Service) ReconcileServiceNowCmdb(ctx context.Context, p *auth.Principal) (api.ServiceNowCmdb, error) {
	if !p.Everywhere(auth.IntegrationsManage) {
		return api.ServiceNowCmdb{}, ErrForbidden
	}
	if c := s.liveConfig(ctx).ServiceNow; !c.Configured() || !c.CMDB {
		return api.ServiceNowCmdb{}, &ConflictError{Msg: "Set ServiceNow's connection and turn its CMDB on first."}
	}
	if err := s.Store.ReconcileServiceNowNow(ctx); err != nil {
		return api.ServiceNowCmdb{}, err
	}
	s.audit(ctx, p, "servicenow.reconcile", "integration", "servicenow", nil, nil)
	return s.ServiceNowCmdb(ctx, p)
}

// testServiceNow signs in and reads one requested item, the table rvav
// reports a request's progress in.
func testServiceNow(ctx context.Context, c config.ServiceNowConfig, roots *x509.CertPool, check checkFunc) {
	if c.URL == "" {
		check("Settings", errors.New("give the instance's URL"), "")
		return
	}
	cl, err := ServiceNowClient(c, roots)
	if !check("Settings", err, "") {
		return
	}
	who := c.Username
	if c.Auth == servicenow.AuthOAuth {
		who = "the OAuth client " + c.ClientID
	}
	_, err = cl.Records(ctx, servicenow.TableRequestedItem, servicenow.Query{Limit: 1, Fields: []string{"sys_id"}})
	check("Requested items", err, fmt.Sprintf("Signed in to %s as %s and read %s.", c.URL, who, servicenow.TableRequestedItem))
}
