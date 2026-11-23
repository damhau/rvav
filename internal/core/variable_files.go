package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// Files in shared variables (ADR-0057): a variable may hold a text file,
// which composite steps read as vars.<group>.<name>.text and cloud-init
// user data as {{ file "<group>.<name>" }}.

// MaxVariableFile is the largest file a variable holds: an AWX job's extra
// variables and a VM's guestinfo carry it whole.
const MaxVariableFile = 256 << 10

// VariableUpload is what an upload of a variable's file sends: the file,
// when it sends one, and a description, when it sends one.
type VariableUpload struct {
	File        bool
	FileName    string
	Content     []byte
	Description *string
}

// A nudge, not a guard, as secretLike: a key belongs in a secret.
var privateKey = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)

// variableFileType is what a file's name says it holds, of the text files
// rvav knows; text/plain otherwise.
func variableFileType(name string) string {
	if k, ok := fileKinds[strings.ToLower(path.Ext(name))]; ok && slices.Contains(k.sniffs, "text/plain") {
		return k.mime
	}
	return "text/plain"
}

func variableFile(v store.Variable) *api.VariableFile {
	if !v.HoldsFile() {
		return nil
	}
	return &api.VariableFile{Name: *v.FileName, Size: *v.FileSize, Sha256: *v.FileSHA256, ContentType: *v.FileType}
}

// fileRecord is a file as steps read it, without its text.
func fileRecord(f *api.VariableFile) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"name": f.Name, "size": f.Size, "sha256": f.Sha256, "content_type": f.ContentType})
	return b
}

// PutVariableFile adds a shared variable that holds a file, or replaces the
// file or the description of one; it says whether it added one. read reads
// the upload, once the caller may.
func (s *Service) PutVariableFile(ctx context.Context, p *auth.Principal, key string, read func() (VariableUpload, error)) (api.Variable, bool, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.Variable{}, false, ErrForbidden
	}
	group, name, _ := strings.Cut(key, ".")
	old, err := s.Store.GetVariable(ctx, group, name)
	create := errors.Is(err, store.ErrNotFound)
	switch {
	case err != nil && !create:
		return api.Variable{}, false, err
	case !create && !old.HoldsFile():
		return api.Variable{}, false, &ConflictError{Msg: key + " holds a value, not a file: change its value, or remove it and add it again."}
	}
	up, err := read()
	if err != nil {
		return api.Variable{}, false, err
	}
	v := store.Variable{Group: group, Name: name, Description: old.Description}
	var errs []api.FieldError
	if create {
		errs = checkVariableKey(group, name)
	}
	var content []byte
	if up.File {
		f, fileErrs, err := checkVariableFile(up)
		if err != nil {
			return api.Variable{}, false, err
		}
		errs = append(errs, fileErrs...)
		if f != nil {
			content, v.Value = up.Content, fileRecord(f)
			v.FileName, v.FileType, v.FileSize, v.FileSHA256 = &f.Name, &f.ContentType, &f.Size, &f.Sha256
		}
	} else if create {
		errs = append(errs, fieldErr("/file", "required", "Upload the file the variable holds."))
	}
	if up.Description != nil {
		if v.Description = strings.TrimSpace(*up.Description); utf8.RuneCountInString(v.Description) > 500 {
			errs = append(errs, fieldErr("/description", "too_long", "At most 500 characters."))
		}
	}
	if !create && !up.File && up.Description == nil {
		return api.Variable{}, false, &BadRequestError{Msg: "Send the new file, a description, or both."}
	}
	if len(errs) > 0 {
		return api.Variable{}, false, &ValidationError{Errors: errs}
	}
	if create {
		switch err := s.Store.AddVariableFile(ctx, v, content, p.User.ID); {
		case errors.Is(err, store.ErrInUse):
			return api.Variable{}, false, &ConflictError{Msg: "There is a variable " + key + " already: upload its file again."}
		case err != nil:
			return api.Variable{}, false, err
		}
		s.audit(ctx, p, "variable.create", "variable", key, nil, map[string]any{"file": v.Value})
	} else {
		if err := s.Store.SetVariableFile(ctx, v, content, p.User.ID); errors.Is(err, store.ErrNotFound) {
			return api.Variable{}, false, ErrNotFound
		} else if err != nil {
			return api.Variable{}, false, err
		}
		detail := map[string]any{"description": v.Description}
		if content != nil {
			detail = map[string]any{"file": v.Value, "was": old.Value}
		}
		s.audit(ctx, p, "variable.update", "variable", key, nil, detail)
	}
	out, err := s.variable(ctx, key)
	return out, create, err
}

// checkVariableFile checks an uploaded file: a text file, not empty, of
// MaxVariableFile at most, with no private key in it.
func checkVariableFile(up VariableUpload) (*api.VariableFile, []api.FieldError, error) {
	name, ok := cleanFileName(up.FileName)
	switch {
	case !ok:
		return nil, nil, &BadRequestError{Msg: "Give the file's own name, without folders or control characters."}
	case len(up.Content) > MaxVariableFile:
		return nil, nil, &TooLargeError{Msg: fmt.Sprintf("%s is larger than %d KiB, the most a variable holds.", name, MaxVariableFile>>10)}
	case len(up.Content) == 0:
		return nil, nil, &BadRequestError{Msg: name + " is empty."}
	case !utf8.Valid(up.Content) || bytes.IndexByte(up.Content, 0) >= 0:
		return nil, nil, &UnsupportedTypeError{Msg: name + " isn't a text file: a variable holds text (UTF-8), which steps and cloud-init read as it is."}
	case privateKey.Match(up.Content):
		return nil, []api.FieldError{fieldErr("/file", "secret",
			name+" holds a private key: a variable isn't masked anywhere, so store the key as a secret instead.")}, nil
	}
	sum := sha256.Sum256(up.Content)
	return &api.VariableFile{Name: name, Size: int64(len(up.Content)), Sha256: hex.EncodeToString(sum[:]),
		ContentType: variableFileType(name)}, nil, nil
}

// VariableFile is the file a shared variable holds, and its content.
func (s *Service) VariableFile(ctx context.Context, p *auth.Principal, key string) (api.VariableFile, []byte, error) {
	if !readsRunData(p) {
		return api.VariableFile{}, nil, ErrForbidden
	}
	group, name, _ := strings.Cut(key, ".")
	v, err := s.Store.GetVariable(ctx, group, name)
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && !v.HoldsFile():
		return api.VariableFile{}, nil, ErrNotFound
	case err != nil:
		return api.VariableFile{}, nil, err
	}
	content, err := s.Store.VariableFileContent(ctx, group, name)
	if errors.Is(err, store.ErrNotFound) {
		return api.VariableFile{}, nil, ErrNotFound
	}
	return *variableFile(v), content, err
}
