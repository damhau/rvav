package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// Files attached to file fields (ADR-0040).

const (
	// MaxFileSize is the most any file field takes; the upload endpoint
	// reads no more.
	MaxFileSize     = 25 << 20
	defaultFileMB   = 10
	maxWaitingFiles = 20
	// MaxImageSize is the largest picture an image field shows.
	MaxImageSize = 2 << 20
	imagePrefix  = "upload:"
)

// imageKinds are the pictures an image field shows: raster only, as an SVG
// can hold scripts.
var imageKinds = []string{".png", ".jpg", ".jpeg", ".gif", ".webp"}

// fileKind is a type of file rvav takes: its MIME type, and what
// http.DetectContentType may say of a file of that type.
type fileKind struct {
	mime   string
	sniffs []string
}

var (
	textSniffs = []string{"text/plain"}
	zipSniffs  = []string{"application/zip"}
	// fileKinds are the types rvav takes, by extension. HTML and SVG are not
	// among them: a browser runs what they hold.
	fileKinds = map[string]fileKind{
		".pdf":  {"application/pdf", []string{"application/pdf"}},
		".png":  {"image/png", []string{"image/png"}},
		".jpg":  {"image/jpeg", []string{"image/jpeg"}},
		".jpeg": {"image/jpeg", []string{"image/jpeg"}},
		".gif":  {"image/gif", []string{"image/gif"}},
		".webp": {"image/webp", []string{"image/webp"}},
		".txt":  {"text/plain", textSniffs},
		".log":  {"text/plain", textSniffs},
		".csv":  {"text/csv", textSniffs},
		".md":   {"text/markdown", textSniffs},
		".json": {"application/json", textSniffs},
		".yaml": {"application/yaml", textSniffs},
		".yml":  {"application/yaml", textSniffs},
		".xml":  {"application/xml", []string{"text/xml", "text/plain"}},
		".sh":   {"application/x-sh", textSniffs},
		".ps1":  {"text/x-powershell", textSniffs},
		".pem":  {"application/x-pem-file", textSniffs},
		".crt":  {"application/x-x509-ca-cert", []string{"text/plain", "application/octet-stream"}},
		".cer":  {"application/x-x509-ca-cert", []string{"text/plain", "application/octet-stream"}},
		".zip":  {"application/zip", zipSniffs},
		".gz":   {"application/gzip", []string{"application/x-gzip"}},
		".tgz":  {"application/gzip", []string{"application/x-gzip"}},
		".docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", zipSniffs},
		".xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", zipSniffs},
		".pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", zipSniffs},
	}
)

// acceptsType says whether an entry of a field's accept list (an
// extension, a MIME type or a family such as image/*) stands for a MIME
// type: an extension stands for its type, so .jpg takes .jpeg files too.
func acceptsType(entry, mime string) bool {
	entry = strings.ToLower(strings.TrimSpace(entry))
	if k, ok := fileKinds[entry]; ok {
		return k.mime == mime
	}
	if family, ok := strings.CutSuffix(entry, "/*"); ok {
		return strings.HasPrefix(mime, family+"/")
	}
	return entry == mime
}

func knownAccept(entry string) bool {
	for _, k := range fileKinds {
		if acceptsType(entry, k.mime) {
			return true
		}
	}
	return false
}

func fileAccepted(f api.UiField, kind fileKind) bool {
	return slices.ContainsFunc(derefSlice(f.Accept), func(a string) bool { return acceptsType(a, kind.mime) })
}

func maxFileMB(f api.UiField) int {
	if f.MaxSizeMb != nil {
		return *f.MaxSizeMb
	}
	return defaultFileMB
}

func maxFileBytes(f api.UiField) int64 { return int64(maxFileMB(f)) << 20 }

// takes says what a file field takes, for a message.
func takes(f api.UiField) string {
	return fmt.Sprintf("%s takes %s files of %d MB at most.", f.Label, strings.Join(derefSlice(f.Accept), ", "), maxFileMB(f))
}

// cleanFileName is a file's own name, without the folders a browser may
// send with it; false for none, or one with control characters.
func cleanFileName(name string) (string, bool) {
	name = strings.TrimSpace(path.Base(strings.ReplaceAll(name, `\`, "/")))
	if name == "" || name == "." || name == "/" || len(name) > 255 || !utf8.ValidString(name) ||
		strings.ContainsFunc(name, unicode.IsControl) {
		return "", false
	}
	return name, true
}

// sniffed is what a file's first bytes say it is, without parameters.
func sniffed(content []byte) string {
	t, _, _ := strings.Cut(http.DetectContentType(content), ";")
	return t
}

func fileValue(f store.RequestFile) map[string]any {
	return map[string]any{"id": f.ID, "name": f.Name, "size": f.Size, "content_type": f.ContentType, "sha256": f.SHA256}
}

// UploadFormFile keeps a file for a form's file field until its request is
// submitted: the field must take its type and size, and its content must
// be what its name says. size is what the client announced, -1 if nothing.
func (s *Service) UploadFormFile(ctx context.Context, p *auth.Principal, q api.UploadFormFileParams, size int64, body io.Reader) (api.FileRef, error) {
	field, err := s.fileField(ctx, p, q)
	if err != nil {
		return api.FileRef{}, err
	}
	name, ok := cleanFileName(q.Name)
	if !ok {
		return api.FileRef{}, &BadRequestError{Msg: "Give the file's own name, without folders or control characters."}
	}
	limit := maxFileBytes(field)
	tooLarge := &TooLargeError{Msg: fmt.Sprintf("%s is larger than %d MB, the most %s takes.", name, maxFileMB(field), field.Label)}
	if size > limit {
		return api.FileRef{}, tooLarge
	}
	ext := strings.ToLower(path.Ext(name))
	kind, known := fileKinds[ext]
	if !known || !fileAccepted(field, kind) {
		return api.FileRef{}, &UnsupportedTypeError{Msg: takes(field)}
	}
	waiting, err := s.Store.DropWaitingFiles(ctx, p.User.ID)
	if err != nil {
		return api.FileRef{}, err
	}
	if waiting >= maxWaitingFiles {
		return api.FileRef{}, &ConflictError{Msg: fmt.Sprintf("You have %d files waiting for their request: submit it first. A file left waiting goes after a day.", waiting)}
	}
	content, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return api.FileRef{}, err
	}
	switch {
	case int64(len(content)) > limit:
		return api.FileRef{}, tooLarge
	case len(content) == 0:
		return api.FileRef{}, &BadRequestError{Msg: name + " is empty."}
	}
	if got := sniffed(content); !slices.Contains(kind.sniffs, got) {
		return api.FileRef{}, &UnsupportedTypeError{Msg: fmt.Sprintf("%s isn't what a %s file holds: its content is %s.", name, ext, got)}
	}
	sum := sha256.Sum256(content)
	f := store.RequestFile{ID: ids.New(ids.File), OwnerID: p.User.ID, Name: name, ContentType: kind.mime,
		Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	if err := s.Store.PutRequestFile(ctx, f, content); err != nil {
		return api.FileRef{}, err
	}
	return api.FileRef{Id: f.ID, Name: f.Name, Size: f.Size, ContentType: f.ContentType, Sha256: f.SHA256}, nil
}

// fileField is the file field a file is attached to, for someone who may
// request the item in the project; a catalog admin's preview attaches to
// any version.
func (s *Service) fileField(ctx context.Context, p *auth.Principal, q api.UploadFormFileParams) (api.UiField, error) {
	ver, err := s.Store.GetVersion(ctx, q.VersionId)
	if errors.Is(err, store.ErrNotFound) {
		return api.UiField{}, ErrNotFound
	}
	if err != nil {
		return api.UiField{}, err
	}
	if !p.Everywhere(auth.CatalogManage) {
		proj, err := s.visibleProject(ctx, p, q.ProjectId)
		if err != nil {
			return api.UiField{}, err
		}
		if !p.In(auth.RequestSubmit, proj.ID) {
			return api.UiField{}, fmt.Errorf("%w: you can view %s but not request in it", ErrForbidden, proj.Name)
		}
		if ok, err := s.Store.IsEntitled(ctx, ver.ItemID, proj.ID); err != nil {
			return api.UiField{}, err
		} else if !ok || ver.State != "published" {
			return api.UiField{}, ErrNotFound
		}
	}
	var ui api.UiSchema
	if err := json.Unmarshal(ver.UISchema, &ui); err != nil {
		return api.UiField{}, err
	}
	f, ok := ui.Fields[q.Field]
	if !ok || f.Widget != api.UiWidgetFile {
		return api.UiField{}, &BadRequestError{Msg: fmt.Sprintf("The form has no file field %q.", q.Field)}
	}
	return f, nil
}

// evaluateFiles puts in each file input the record of the file it names:
// one the requester attached that still waits for its request (once
// submitted, ofRequest, the request's), of a type and size the field takes.
func (s *Service) evaluateFiles(ctx context.Context, p *auth.Principal, ev *Evaluation, ui api.UiSchema, ofRequest bool) error {
	for _, name := range sortedKeys(ui.Fields) {
		f := ui.Fields[name]
		v, given := ev.Inputs[name]
		if f.Widget != api.UiWidgetFile || !given || v == nil {
			continue
		}
		at := "/inputs/" + name
		ref, _ := v.(map[string]any)
		id, _ := ref["id"].(string)
		file, err := s.Store.GetRequestFile(ctx, id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err != nil || file.OwnerID != p.User.ID ||
			!ofRequest && (file.RequestID != nil || time.Since(file.CreatedAt) > store.FileWait) {
			ev.Errors = append(ev.Errors, fieldErr(at, "gone", "This file no longer waits for a request: attach it again."))
			continue
		}
		if kind, ok := fileKinds[strings.ToLower(path.Ext(file.Name))]; !ok || !fileAccepted(f, kind) || file.Size > maxFileBytes(f) {
			ev.Errors = append(ev.Errors, fieldErr(at, "not_allowed", takes(f)))
			continue
		}
		ev.Inputs[name] = fileValue(file)
	}
	return nil
}

// attachFiles gives a submitted request the files its inputs name.
func (s *Service) attachFiles(ctx context.Context, db store.DB, requestID, ownerID string, ev *Evaluation) error {
	var ui api.UiSchema
	if err := json.Unmarshal(ev.Version.UISchema, &ui); err != nil {
		return err
	}
	var files []string
	for _, name := range slices.Sorted(maps.Keys(ui.Fields)) {
		if v, ok := ev.Inputs[name].(map[string]any); ok && ui.Fields[name].Widget == api.UiWidgetFile {
			if id, ok := v["id"].(string); ok {
				files = append(files, id)
			}
		}
	}
	if len(files) == 0 {
		return nil
	}
	n, err := s.Store.AttachFiles(ctx, db, requestID, ownerID, files)
	if err == nil && n != int64(len(files)) {
		err = &ConflictError{Msg: "A file went to another request meanwhile: attach it again."}
	}
	return err
}

// DownloadFile is a file and its content, for who may have it: while it
// waits, the one who attached it; then the request's requester and owner,
// its approvers and its project's admins.
func (s *Service) DownloadFile(ctx context.Context, p *auth.Principal, id string) (store.RequestFile, []byte, error) {
	f, err := s.Store.GetRequestFile(ctx, id)
	if err != nil {
		return f, nil, err
	}
	if f.RequestID == nil {
		if f.OwnerID != p.User.ID {
			return f, nil, ErrNotFound
		}
	} else {
		req, err := s.Store.GetRequest(ctx, *f.RequestID)
		if err != nil {
			return f, nil, err
		}
		if ok, err := s.CanReadRequest(ctx, p, req); err != nil || !ok {
			return f, nil, cmpErr(err, ErrNotFound)
		}
		approver, err := s.Store.IsRequestApprover(ctx, req.ID, p.User.ID, p.Groups)
		if err != nil {
			return f, nil, err
		}
		if req.RequesterID != p.User.ID && req.OwnerID != p.User.ID && !approver && !p.In(auth.RequestManage, req.ProjectID) {
			return f, nil, fmt.Errorf("%w: only the requester, the request's approvers and its project's admins can download its files", ErrForbidden)
		}
	}
	content, err := s.Store.RequestFileContent(ctx, id)
	return f, content, err
}

func cmpErr(err, otherwise error) error {
	if err != nil {
		return err
	}
	return otherwise
}

// checkFile checks a file field's design: types rvav knows, a size it
// takes; no approver's edit nor initial value, which would name a file no
// requester attached; and not secret, which a file isn't.
func checkFile(name string, f api.UiField, initial bool, prefix string, publish bool) []api.FieldError {
	if f.Widget != api.UiWidgetFile {
		return nil
	}
	ptr := prefix + "/ui_schema/fields/" + name
	var errs []api.FieldError
	for i, a := range derefSlice(f.Accept) {
		if !knownAccept(a) {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/accept/%d", ptr, i), "unknown",
				fmt.Sprintf("rvav doesn't take %s files. It takes %s.", a, strings.Join(slices.Sorted(maps.Keys(fileKinds)), ", "))))
		}
	}
	if publish && len(derefSlice(f.Accept)) == 0 {
		errs = append(errs, fieldErr(ptr+"/accept", "required", "Say which types of file the field takes."))
	}
	if n := f.MaxSizeMb; n != nil && (*n < 1 || *n > MaxFileSize>>20) {
		errs = append(errs, fieldErr(ptr+"/max_size_mb", "out_of_range", fmt.Sprintf("A file field takes files of 1 to %d MB.", MaxFileSize>>20)))
	}
	if f.ApproverEditable != nil && *f.ApproverEditable {
		errs = append(errs, fieldErr(ptr+"/approver_editable", "not_allowed", "An approver can't change a file the requester attached."))
	}
	if f.Secret != nil && *f.Secret {
		errs = append(errs, fieldErr(ptr+"/secret", "not_allowed", "A file is kept as it is sent, not sealed: a file field can't be secret."))
	}
	if initial {
		errs = append(errs, fieldErr(prefix+"/initial_values/"+name, "not_allowed", "A file field starts empty: the requester attaches the file."))
	}
	return errs
}

// UploadFormImage keeps a picture for image fields, which show it as
// upload:<id>: a raster image whose content is what its name says.
func (s *Service) UploadFormImage(ctx context.Context, p *auth.Principal, name string, size int64, body io.Reader) (api.FormImage, error) {
	if !p.Everywhere(auth.CatalogManage) {
		return api.FormImage{}, fmt.Errorf("%w: only catalog administrators upload images for forms", ErrForbidden)
	}
	name, ok := cleanFileName(name)
	if !ok {
		return api.FormImage{}, &BadRequestError{Msg: "Give the picture's own name, without folders or control characters."}
	}
	ext := strings.ToLower(path.Ext(name))
	if !slices.Contains(imageKinds, ext) {
		return api.FormImage{}, &UnsupportedTypeError{Msg: "An image field shows PNG, JPEG, GIF or WebP pictures."}
	}
	tooLarge := &TooLargeError{Msg: fmt.Sprintf("%s is larger than %d MB, the most an image field shows.", name, MaxImageSize>>20)}
	if size > MaxImageSize {
		return api.FormImage{}, tooLarge
	}
	content, err := io.ReadAll(io.LimitReader(body, MaxImageSize+1))
	switch {
	case err != nil:
		return api.FormImage{}, err
	case len(content) > MaxImageSize:
		return api.FormImage{}, tooLarge
	case len(content) == 0:
		return api.FormImage{}, &BadRequestError{Msg: name + " is empty."}
	}
	kind := fileKinds[ext]
	if got := sniffed(content); !slices.Contains(kind.sniffs, got) {
		return api.FormImage{}, &UnsupportedTypeError{Msg: fmt.Sprintf("%s isn't what a %s picture holds: its content is %s.", name, ext, got)}
	}
	sum := sha256.Sum256(content)
	img := store.FormImage{ID: ids.New(ids.Image), Name: name, ContentType: kind.mime, Size: int64(len(content)),
		SHA256: hex.EncodeToString(sum[:]), CreatedBy: &p.User.ID}
	if err := s.Store.PutFormImage(ctx, img, content); err != nil {
		return api.FormImage{}, err
	}
	return api.FormImage{Id: img.ID, Name: img.Name, Size: img.Size, ContentType: img.ContentType, Url: imagePrefix + img.ID}, nil
}

// FormImage is a picture image fields show, for anyone signed in.
func (s *Service) FormImage(ctx context.Context, id string) (store.FormImage, []byte, error) {
	return s.Store.GetFormImage(ctx, id)
}

// uploadedImage is the picture an image field's url names as upload:<id>.
func uploadedImage(f api.UiField) (string, bool) {
	return strings.CutPrefix(strings.TrimSpace(deref(f.Url)), imagePrefix)
}

// checkImages refuses an image field showing a picture rvav doesn't keep.
func (s *Service) checkImages(ctx context.Context, d api.CatalogVersionDefinition, prefix string) ([]api.FieldError, error) {
	at := map[string]string{}
	for _, name := range sortedKeys(d.UiSchema.Fields) {
		if id, ok := uploadedImage(d.UiSchema.Fields[name]); ok && d.UiSchema.Fields[name].Widget == api.UiWidgetImage {
			at[id] = prefix + "/ui_schema/fields/" + name + "/url"
		}
	}
	if len(at) == 0 {
		return nil, nil
	}
	missing, err := s.Store.MissingFormImages(ctx, slices.Sorted(maps.Keys(at)))
	var errs []api.FieldError
	for _, id := range missing {
		errs = append(errs, fieldErr(at[id], "unknown", fmt.Sprintf("rvav keeps no image %s: upload it again.", id)))
	}
	return errs, err
}
