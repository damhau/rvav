package main

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vapi/tags"
)

// TestServe: the image's simulator reports healthy only once the GVA
// layout is made, tags included (the vapi endpoint), as the compose
// stack's and kind's health checks expect.
func TestServe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := healthcheck(ctx, "127.0.0.1:1"); err == nil {
		t.Fatal("healthy with nothing listening")
	}
	ready := make(chan *url.URL, 1)
	done := make(chan error, 1)
	sctx, stop := context.WithCancel(ctx)
	go func() { done <- serve(sctx, "127.0.0.1:0", func(u *url.URL) { ready <- u }) }()
	var u *url.URL
	select {
	case u = <-ready:
	case err := <-done:
		t.Fatalf("serve: %v", err)
	}
	if err := healthcheck(ctx, u.Host); err != nil {
		t.Fatalf("healthcheck: %v", err)
	}

	c, err := govmomi.NewClient(ctx, u, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := find.NewFinder(c.Client).VirtualMachine(ctx, "/GVA/vm/templates/tpl-rhel9.4-2026.10"); err != nil {
		t.Errorf("template: %v", err)
	}
	rc := rest.NewClient(c.Client)
	if err := rc.Login(ctx, u.User); err != nil {
		t.Fatal(err)
	}
	if _, err := tags.NewManager(rc).GetCategory(ctx, "env"); err != nil {
		t.Errorf("tag category env: %v", err)
	}

	stop()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
}
