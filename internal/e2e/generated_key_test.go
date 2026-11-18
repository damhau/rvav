package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/provision"
)

// cloudInitDriver stands in for cloud-init on a real worker, as
// cloudInitGuest does for the test workflow environment: a VM powered on
// gets the address its guestinfo metadata gives its first adapter, or
// dhcpLease when that adapter is on DHCP.
type cloudInitDriver struct {
	vsphere.Driver
	c *vim25.Client
}

func (d cloudInitDriver) PowerOn(ctx context.Context, moref string) error {
	if err := d.Driver.PowerOn(ctx, moref); err != nil {
		return err
	}
	ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: moref}
	info, err := guestInfoOf(ctx, d.c, ref)
	if err != nil {
		return err
	}
	raw, _ := base64.StdEncoding.DecodeString(info["guestinfo.metadata"])
	var meta struct {
		Network struct {
			Ethernets map[string]struct {
				Addresses []string
				DHCP4     bool `json:"dhcp4"`
			}
		}
	}
	_ = json.Unmarshal(raw, &meta)
	nic := meta.Network.Ethernets["nic0"]
	ip := dhcpLease
	switch {
	case len(nic.Addresses) > 0:
		ip, _, _ = strings.Cut(nic.Addresses[0], "/")
	case !nic.DHCP4:
		return nil
	}
	task, err := object.NewVirtualMachine(d.c, ref).Reconfigure(ctx, types.VirtualMachineConfigSpec{
		ExtraConfig: []types.BaseOptionValue{&types.OptionValue{Key: "SET.guest.ipAddress", Value: ip}}})
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// TestGeneratedKey: rvav makes a key pair for a deployment and puts its
// public key on the VMs; the owner downloads the private key once, and
// neither Temporal's history, the events, the audit log nor the request
// ever hold it. Deleting the deployment forgets a key not downloaded
// (ADR-0039).
func TestGeneratedKey(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := accessHarness{setup(t, ctx, c), t}
		const taskQueue = "rvav-access-e2e"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		h.acts.VSphere = cloudInitDriver{Driver: h.acts.VSphere, c: c}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		item := h.publish("gen-key", accessItem(map[string]any{"kind": "generated_key", "username": "ops"}))
		build := func(role string) (api.Request, string) {
			t.Helper()
			req := h.submit(item, role, nil)
			req = h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
			wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := tc.GetWorkflow(wctx, provision.WorkflowID(req.Id), "").Get(wctx, nil); err != nil {
				t.Fatal(err)
			}
			return req, (*req.DeploymentIds)[0]
		}
		deployment := func(id string) api.DeploymentDetail {
			t.Helper()
			var d api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+id, "tnguyen", nil, 200, &d)
			return d
		}
		remove := func(id string) {
			t.Helper()
			var del api.Request
			h.json("POST", "/api/v1/deployments/"+id+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
				202, &del, "Idempotency-Key", uuid.NewString())
			h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		}

		req, id := build("gen")
		d := deployment(id)
		gk := d.GeneratedKey
		if gk == nil || !gk.Available || gk.Username != "ops" || !strings.HasPrefix(gk.Fingerprint, "SHA256:") || gk.DownloadedAt != nil {
			t.Fatalf("generated key = %+v", gk)
		}
		if ra := d.RemoteAccess; ra == nil || len(*ra) != 1 || (*ra)[0].Kind != api.RemoteAccessKindGeneratedKey ||
			(*ra)[0].Component != "vm" || deref((*ra)[0].Fingerprint) != gk.Fingerprint {
			t.Fatalf("remote access = %+v", d.RemoteAccess)
		}
		user, _ := h.userData(ctx, c, req.Id)
		ops, _ := user["users"].([]any)[1].(map[string]any)
		keys, _ := ops["ssh_authorized_keys"].([]any)
		if len(keys) != 1 {
			t.Fatalf("the user = %v", ops)
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(keys[0].(string)))
		if err != nil || ssh.FingerprintSHA256(pub) != gk.Fingerprint {
			t.Fatalf("the VM's key %v: %v", keys[0], err)
		}

		// The owner's only, even for an administrator, and once.
		var problem api.Problem
		h.json("POST", "/api/v1/deployments/"+id+"/private-key", "mkeller", nil, 403, &problem)
		if !strings.Contains(deref(problem.Detail), "owner, Thomas Nguyen") {
			t.Fatalf("an administrator's download = %+v", problem)
		}
		var key api.PrivateKey
		header := h.json("POST", "/api/v1/deployments/"+id+"/private-key", "tnguyen", nil, 200, &key)
		if header.Get("Cache-Control") != "no-store" {
			t.Fatalf("Cache-Control = %q", header.Get("Cache-Control"))
		}
		signer, err := ssh.ParsePrivateKey([]byte(key.PrivateKey))
		if err != nil || ssh.FingerprintSHA256(signer.PublicKey()) != gk.Fingerprint || key.Username != "ops" {
			t.Fatalf("the private key: %v %+v", err, key)
		}
		h.json("POST", "/api/v1/deployments/"+id+"/private-key", "tnguyen", nil, 409, &problem)
		if !strings.Contains(deref(problem.Detail), "Thomas Nguyen downloaded the private key") {
			t.Fatalf("a second download = %+v", problem)
		}
		gk = deployment(id).GeneratedKey
		if gk.Available || gk.DownloadedAt == nil || gk.DownloadedBy == nil || gk.DownloadedBy.Name != "Thomas Nguyen" {
			t.Fatalf("downloaded = %+v", gk)
		}
		var audited int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where action = 'deployment.private_key.download'
			and target_id = $1`, id).Scan(&audited)
		if audited != 1 {
			t.Fatalf("%d downloads audited", audited)
		}

		// Nowhere else: a line of the key's own bytes (the first ones are
		// the format's), in rvav's tables and in the workflow's history.
		lines := strings.Split(strings.TrimSpace(key.PrivateKey), "\n")
		secret := lines[len(lines)/2]
		if n := h.leaks(ctx, secret); n != 0 {
			t.Fatalf("the private key is kept in %d places", n)
		}
		var kept int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from deployment_keys where ciphertext is not null`).Scan(&kept)
		if kept != 0 {
			t.Fatalf("%d private keys still sealed", kept)
		}
		iter := tc.GetWorkflowHistory(ctx, provision.WorkflowID(req.Id), "", false, 0)
		events := 0
		for iter.HasNext() {
			ev, err := iter.Next()
			if err != nil {
				t.Fatal(err)
			}
			events++
			if s := ev.String(); strings.Contains(s, secret) || strings.Contains(s, "PRIVATE KEY") {
				t.Fatalf("history event %d holds the private key: %s", ev.EventId, ev.EventType)
			}
		}
		if events < 10 {
			t.Fatalf("the history has %d events", events)
		}

		remove(id)
		var left int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1 and type = 'guest.access'`, id).Scan(&left)
		if left != 0 {
			t.Fatalf("%d guest.access resources left", left)
		}

		// A key not downloaded is forgotten with the deployment.
		_, other := build("gen2")
		if deployment(other).GeneratedKey.Fingerprint == gk.Fingerprint {
			t.Fatal("two deployments share a key pair")
		}
		remove(other)
		if gk := deployment(other).GeneratedKey; gk == nil || gk.Available || gk.DownloadedAt != nil {
			t.Fatalf("after the delete = %+v", gk)
		}
		h.json("POST", "/api/v1/deployments/"+other+"/private-key", "tnguyen", nil, 409, &problem)
		if !strings.Contains(deref(problem.Detail), "forgotten") {
			t.Fatalf("a download after the delete = %+v", problem)
		}
		settle(t, ctx, tc, req.Id)
	})
}
