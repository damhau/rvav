package provision

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type part struct{ typ, body string }

func multipartParts(t *testing.T, doc string) []part {
	t.Helper()
	header, body, ok := strings.Cut(doc, "\r\n\r\n")
	if !ok {
		t.Fatalf("no header:\n%s", doc)
	}
	_, params, err := mime.ParseMediaType(strings.TrimPrefix(strings.Split(header, "\r\n")[0], "Content-Type: "))
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(strings.NewReader(body), params["boundary"])
	var out []part
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		out = append(out, part{p.Header.Get("Content-Type"), string(b)})
	}
}

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl ops@laptop"

// TestAccessUserData: the remote access's user joins the template's and
// the item's user data; the distribution's default user stays unless they
// list users (ADR-0039).
func TestAccessUserData(t *testing.T) {
	key := &access{kind: "ssh_key", username: "ops", publicKey: testKey}
	parse := func(s string) map[string]any {
		t.Helper()
		if !strings.HasPrefix(s, "#cloud-config\n") {
			t.Fatalf("not a cloud-config document:\n%s", s)
		}
		var m map[string]any
		if err := yaml.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	user := map[string]any{"name": "ops", "shell": "/bin/bash", "sudo": "ALL=(ALL) NOPASSWD:ALL", "lock_passwd": true,
		"ssh_authorized_keys": []any{testKey}}

	got, err := userData("", "", key)
	if err != nil {
		t.Fatal(err)
	}
	if m := parse(got); !reflect.DeepEqual(m["users"], []any{"default", user}) {
		t.Fatalf("alone: users = %v", m["users"])
	}

	got, err = userData("#cloud-config\npackages: [chrony]\n", "#cloud-config\nusers:\n  - name: app\n", key)
	if err != nil {
		t.Fatal(err)
	}
	m := parse(got)
	if !reflect.DeepEqual(m["users"], []any{map[string]any{"name": "app"}, user}) || !reflect.DeepEqual(m["packages"], []any{"chrony"}) {
		t.Fatalf("merged: %v", m)
	}

	// A script: a part of its own after it, appended to what it set.
	got, err = userData("#!/bin/sh\necho hi\n", "", key)
	if err != nil {
		t.Fatal(err)
	}
	parts := multipartParts(t, got)
	if len(parts) != 2 || parts[0].body != "#!/bin/sh\necho hi\n" || !strings.Contains(parts[1].body, "merge_how: list(append)") ||
		!strings.Contains(parts[1].body, "- default") {
		t.Fatalf("with a script: %+v", parts)
	}

	pw := &access{kind: "password", username: "ops", password: "Pa55-word"}
	got, err = userData("", "#cloud-config\nusers: [default]\n", pw)
	if err != nil {
		t.Fatal(err)
	}
	m = parse(got)
	want := []any{"default", map[string]any{"name": "ops", "shell": "/bin/bash", "sudo": "ALL=(ALL) NOPASSWD:ALL",
		"lock_passwd": false, "plain_text_passwd": "Pa55-word"}}
	if !reflect.DeepEqual(m["users"], want) || m["ssh_pwauth"] != true {
		t.Fatalf("a password: %v", m)
	}
	if strings.Contains(pw.describe(), "Pa55") || strings.Contains(key.describe(), "AAAA") {
		t.Fatal("a step's detail would show the password or the key")
	}

	meta, err := withCleanup([]byte(`{"instance-id":"d/vm"}`))
	if err != nil {
		t.Fatal(err)
	}
	var mm map[string]any
	_ = json.Unmarshal(meta, &mm)
	if !reflect.DeepEqual(mm["cleanup-guestinfo"], []any{"userdata"}) || mm["instance-id"] != "d/vm" {
		t.Fatalf("metadata = %s", meta)
	}
}
