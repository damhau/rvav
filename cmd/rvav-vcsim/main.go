// Command rvav-vcsim runs the govmomi vCenter simulator with the GVA layout
// used by the seed data, for local development without a real vCenter.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/simulator"

	"github.com/damhau/rvav/internal/integrations/vsphere"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:8989", "address to listen on")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	model := simulator.VPX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		log.Fatalf("create model: %v", err)
	}
	model.Service.Listen = &url.URL{Host: *addr}
	model.Service.TLS = new(tls.Config) // self-signed, like a fresh vCenter
	srv := model.Service.NewServer()
	defer srv.Close()

	c, err := govmomi.NewClient(ctx, srv.URL, true)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	if err := vsphere.Bootstrap(ctx, c.Client, vsphere.DefaultLayout()); err != nil {
		log.Fatalf("bootstrap: %v", err)
	}
	u := *srv.URL
	pass, _ := u.User.Password()
	fmt.Printf("vcsim ready with the GVA layout\n  RVAV_VCENTER_URL=%s://%s/sdk\n  RVAV_VCENTER_USERNAME=%s\n  RVAV_VCENTER_PASSWORD=%s\n  RVAV_VCENTER_INSECURE=1\n",
		u.Scheme, u.Host, u.User.Username(), pass)
	<-ctx.Done()
}
