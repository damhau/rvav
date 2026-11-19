// Command terraform-provider-rvav is rvav's Terraform provider
// (docs/guides/terraform.md, ADR-0053). Terraform starts it; -debug runs it
// for a debugger, printing the TF_REATTACH_PROVIDERS to set.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/damhau/rvav/internal/buildinfo"
	"github.com/damhau/rvav/internal/tfprovider"
)

func main() {
	debug := flag.Bool("debug", false, "run for a debugger")
	flag.Parse()
	err := providerserver.Serve(context.Background(), tfprovider.New(buildinfo.Version()),
		providerserver.ServeOpts{Address: tfprovider.Address, Debug: *debug})
	if err != nil {
		log.Fatal(err)
	}
}
