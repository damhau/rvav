package core

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const sharedFile = `apiVersion: rvav.dev/v1
kind: CatalogItem
metadata:
  slug: linux
  name: Linux
  summary: A VM.
  category: compute
  type: vm_blueprint
  icon: server
sharing:
  projects:
    - project: web
      users:
        - lea.martin@corp.example
      groups:
        - rvav-web
versions:
  - number: "1.0"
    form_schema: {}
    ui_schema: {}
    blueprint: {}
`

// A file's sharing reads back as written, and the repository's file
// differs in its details when it has a sharing block rvav's never has.
func TestSharingInFiles(t *testing.T) {
	var doc itemDoc
	if err := yaml.Unmarshal([]byte(sharedFile), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Sharing == nil || len(doc.Sharing.Projects) != 1 || doc.Sharing.Projects[0].Project != "web" ||
		doc.Sharing.Projects[0].Audience.Users[0] != "lea.martin@corp.example" || doc.Sharing.Projects[0].Audience.Groups[0] != "rvav-web" {
		t.Fatalf("sharing = %+v", doc.Sharing)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "sharing:\n    projects:\n        - project: web\n          users:\n") {
		t.Fatalf("written back:\n%s", out)
	}
	rvavs := strings.Replace(sharedFile, sharedFile[strings.Index(sharedFile, "sharing:"):strings.Index(sharedFile, "versions:")], "", 1)
	if def, meta := sameFile([]byte(rvavs), []byte(sharedFile)); !def || meta {
		t.Fatalf("same definition %v, same details %v", def, meta)
	}
	if def, meta := sameFile([]byte(rvavs), []byte(rvavs)); !def || !meta {
		t.Fatalf("the same file: %v %v", def, meta)
	}
}
