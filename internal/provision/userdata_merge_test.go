package provision

import (
	"io"
	"mime"
	"mime/multipart"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestMergeUserData: a template's user data and an item's (ADR-0037).
func TestMergeUserData(t *testing.T) {
	image := "#cloud-config\npackages: [chrony, qualys-agent]\nruncmd:\n  - [systemctl, enable, --now, chronyd]\n" +
		"ntp:\n  enabled: true\n  servers: [ntp1.corp.example]\ntimezone: UTC\n"
	own := "#cloud-config\npackages: [nginx]\nruncmd:\n  - echo web > /etc/motd\nntp:\n  servers: [ntp2.corp.example]\n" +
		"timezone: Europe/Zurich\nwrite_files:\n  - path: /etc/app.conf\n    content: |\n      a=1\n      b=2\n"

	got, err := mergeUserData(image, own)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "#cloud-config\n") {
		t.Fatalf("not a cloud-config document:\n%s", got)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"packages":    []any{"chrony", "qualys-agent", "nginx"},
		"runcmd":      []any{[]any{"systemctl", "enable", "--now", "chronyd"}, "echo web > /etc/motd"},
		"ntp":         map[string]any{"enabled": true, "servers": []any{"ntp1.corp.example", "ntp2.corp.example"}},
		"timezone":    "Europe/Zurich",
		"write_files": []any{map[string]any{"path": "/etc/app.conf", "content": "a=1\nb=2\n"}},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("merged:\n%s", got)
	}

	for _, c := range []struct{ image, own, want string }{
		{"", own, own},
		{image, "", image},
	} {
		if got, err := mergeUserData(c.image, c.own); err != nil || got != c.want {
			t.Fatalf("one side only: %q, %v", got, err)
		}
	}

	if _, err := mergeUserData(image, "#cloud-config\n- a list\n"); err == nil || !strings.Contains(err.Error(), "the item's") {
		t.Fatalf("an item's list, not a map: %v", err)
	}
}

// TestMergeUserDataParts: a script and a cloud-config document go as two
// parts, the template's first.
func TestMergeUserDataParts(t *testing.T) {
	image := "#!/bin/sh\necho base > /etc/base\n"
	own := "#cloud-config\npackages: [nginx]\n"
	got, err := mergeUserData(image, own)
	if err != nil {
		t.Fatal(err)
	}
	header, body, ok := strings.Cut(got, "\r\n\r\n")
	if !ok {
		t.Fatalf("no header:\n%s", got)
	}
	mt, params, err := mime.ParseMediaType(strings.TrimPrefix(strings.Split(header, "\r\n")[0], "Content-Type: "))
	if err != nil || mt != "multipart/mixed" || !strings.Contains(header, "MIME-Version: 1.0") {
		t.Fatalf("header %q: %v", header, err)
	}
	r := multipart.NewReader(strings.NewReader(body), params["boundary"])
	var types, docs []string
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		types, docs = append(types, p.Header.Get("Content-Type")), append(docs, string(b))
	}
	if !reflect.DeepEqual(types, []string{`text/x-shellscript; charset="utf-8"`, `text/cloud-config; charset="utf-8"`}) ||
		!reflect.DeepEqual(docs, []string{image, own}) {
		t.Fatalf("parts %v %q", types, docs)
	}
}
