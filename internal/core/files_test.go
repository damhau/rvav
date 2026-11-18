package core

import (
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/api"
)

func TestFileTypes(t *testing.T) {
	field := api.UiField{Widget: api.UiWidgetFile, Label: "Rules", Accept: &[]string{".csv", "application/PDF", "image/*"}}
	for ext, want := range map[string]bool{".csv": true, ".pdf": true, ".png": true, ".jpeg": true, ".txt": false, ".zip": false} {
		if got := fileAccepted(field, fileKinds[ext]); got != want {
			t.Errorf("%s accepted = %v, want %v", ext, got, want)
		}
	}
	if !fileAccepted(api.UiField{Accept: &[]string{".jpg"}}, fileKinds[".jpeg"]) {
		t.Error(".jpg doesn't take a .jpeg file, which is of the same type")
	}
	for entry, want := range map[string]bool{".pdf": true, " .PNG ": true, "text/*": true, "application/pdf": true,
		".html": false, ".svg": false, "image/svg+xml": false, "video/*": false, ".exe": false} {
		if got := knownAccept(entry); got != want {
			t.Errorf("knownAccept(%q) = %v, want %v", entry, got, want)
		}
	}

	pdf, png := []byte("%PDF-1.7\n1 0 obj\n"), []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	for _, c := range []struct {
		ext     string
		content []byte
		ok      bool
	}{
		{".pdf", pdf, true}, {".png", png, true}, {".png", pdf, false}, {".csv", []byte("host,port\nweb-01,443\n"), true},
		{".csv", png, false}, {".txt", []byte("<!DOCTYPE html><script>alert(1)</script>"), false},
		{".json", []byte(`{"a": 1}`), true}, {".xml", []byte(`<?xml version="1.0"?><a/>`), true},
		{".docx", []byte("PK\x03\x04\x14\x00"), true}, {".pdf", []byte("PK\x03\x04\x14\x00"), false},
	} {
		if got := slices.Contains(fileKinds[c.ext].sniffs, sniffed(c.content)); got != c.ok {
			t.Errorf("a %s file holding %q: taken = %v (sniffed %s)", c.ext, c.content[:8], got, sniffed(c.content))
		}
	}

	for name, want := range map[string]string{"rules.csv": "rules.csv", `C:\Users\me\rules.csv`: "rules.csv",
		"../../etc/passwd": "passwd", "  notes.txt ": "notes.txt", "": "", "a\x00b.txt": "", "/": ""} {
		if got, _ := cleanFileName(name); got != want {
			t.Errorf("cleanFileName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCheckFileField(t *testing.T) {
	codes := func(errs []api.FieldError) []string {
		var out []string
		for _, e := range errs {
			out = append(out, e.Field+" "+deref(e.Code))
		}
		return out
	}
	f := api.UiField{Widget: api.UiWidgetFile, Label: "Rules"}
	if got := codes(checkFile("rules", f, false, "", false)); len(got) != 0 {
		t.Errorf("a draft's file field with no types yet: %v", got)
	}
	if got, want := codes(checkFile("rules", f, false, "", true)), []string{"/ui_schema/fields/rules/accept required"}; !slices.Equal(got, want) {
		t.Errorf("published with no types: %v, want %v", got, want)
	}
	f.Accept, f.MaxSizeMb, f.ApproverEditable, f.Secret = &[]string{".csv", ".html"}, new(26), new(true), new(true)
	want := []string{"/ui_schema/fields/rules/accept/1 unknown", "/ui_schema/fields/rules/max_size_mb out_of_range",
		"/ui_schema/fields/rules/approver_editable not_allowed", "/ui_schema/fields/rules/secret not_allowed", "/initial_values/rules not_allowed"}
	if got := codes(checkFile("rules", f, true, "", true)); !slices.Equal(got, want) {
		t.Errorf("a file field with what it can't have: %v, want %v", got, want)
	}
	if got := checkFile("name", api.UiField{Widget: api.UiWidgetText, Accept: &[]string{".exe"}}, true, "", true); got != nil {
		t.Errorf("a text field is no file field: %v", got)
	}
}
