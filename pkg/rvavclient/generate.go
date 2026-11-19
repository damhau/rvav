// Package rvavclient calls rvav's API as a user: a client generated from
// api/openapi.yaml (`make gen`), the server's address and a bearer token
// (Connect), the API's problem details as errors (Check), and the profiles
// rvavctl signs in to (Profiles). rvavctl uses it, and so can other tools.
//
//	c, err := rvavclient.Connect("https://rvav.corp.example", rvavclient.StaticToken(os.Getenv("RVAV_TOKEN")))
//	me, err := c.GetMeWithResponse(ctx)
//	if err := rvavclient.Check(me.HTTPResponse, me.Body); err != nil { ... }
//	fmt.Println(me.JSON200.User.Name)
package rvavclient

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config oapi-codegen.yaml ../../api/openapi.yaml
