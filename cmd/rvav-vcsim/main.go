// Command rvav-vcsim runs the govmomi vCenter simulator with the GVA layout
// used by the seed data, for local development without a real vCenter. It
// is the image the compose stack and kind run (deploy/README.md).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vmware/govmomi"
	_ "github.com/vmware/govmomi/pbm/simulator" // storage policies
	"github.com/vmware/govmomi/simulator"
	_ "github.com/vmware/govmomi/vapi/simulator" // the tagging service and content libraries

	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/vcsim"
)

// readyPath answers 200 once the layout exists, 503 before.
const readyPath = "/readyz"

func main() {
	addr := flag.String("listen", "127.0.0.1:8989", "address to listen on")
	probe := flag.Bool("healthcheck", false, "exit 0 if the simulator on -listen's port has its layout (the image has no shell or curl)")
	flag.Parse()
	if *probe {
		if err := healthcheck(context.Background(), *addr); err != nil {
			log.Fatal(err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := serve(ctx, *addr, func(u *url.URL) {
		pass, _ := u.User.Password()
		fmt.Printf("vcsim ready with the GVA layout\n  RVAV_VCENTER_URL=%s://%s/sdk\n  RVAV_VCENTER_USERNAME=%s\n  RVAV_VCENTER_PASSWORD=%s\n  RVAV_VCENTER_INSECURE=1\n",
			u.Scheme, u.Host, u.User.Username(), pass)
	})
	if err != nil {
		log.Fatal(err)
	}
}

// serve runs the simulator on addr until ctx ends, and calls ready with its
// URL once the layout is made.
func serve(ctx context.Context, addr string, ready func(*url.URL)) error {
	model := simulator.VPX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		return fmt.Errorf("create model: %w", err)
	}
	vcsim.SerialClones(model.Map())
	vcsim.NonBlockingFilters(model.Map())
	vcsim.CloudInitGuests(model.Map())
	var made atomic.Bool
	model.Service.HandleFunc(readyPath, func(w http.ResponseWriter, _ *http.Request) {
		if !made.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	model.Service.RegisterEndpoints = true
	model.Service.Listen = &url.URL{Host: addr}
	model.Service.TLS = new(tls.Config) // self-signed, like a fresh vCenter
	srv := model.Service.NewServer()
	defer srv.Close()

	c, err := govmomi.NewClient(ctx, srv.URL, true)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if err := vsphere.Bootstrap(ctx, c.Client, vsphere.DefaultLayout()); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	made.Store(true)
	ready(srv.URL)
	<-ctx.Done()
	return nil
}

// healthcheck asks the simulator listening on addr's port whether its
// layout is made.
func healthcheck(ctx context.Context, addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort("127.0.0.1", port)+readyPath, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // its own self-signed certificate, on the loopback
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("not ready: " + resp.Status)
	}
	return nil
}
