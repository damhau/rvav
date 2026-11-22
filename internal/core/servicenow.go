package core

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/servicenow"
)

// ServiceNow (ADR-0065): the instance rvav keeps CIs in, and takes requests
// from.

// ServiceNowClient is a client of the instance the settings name.
func ServiceNowClient(c config.ServiceNowConfig, roots *x509.CertPool) (*servicenow.Client, error) {
	o := c.TLS()
	o.Roots = roots
	return servicenow.New(servicenow.Config{URL: c.URL, Auth: c.Auth, Username: c.Username, ClientID: c.ClientID, Secret: c.Secret, TLS: o})
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
