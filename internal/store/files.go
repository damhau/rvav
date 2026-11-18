package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// RequestFile is a file attached to a file field (ADR-0040): waiting for
// its request while RequestID is nil.
type RequestFile struct {
	ID          string    `db:"id"`
	OwnerID     string    `db:"owner_id"`
	RequestID   *string   `db:"request_id"`
	Name        string    `db:"name"`
	ContentType string    `db:"content_type"`
	Size        int64     `db:"size"`
	SHA256      string    `db:"sha256"`
	CreatedAt   time.Time `db:"created_at"`
}

const requestFileCols = `id, owner_id, request_id, name, content_type, size, sha256, created_at`

// FileWait is how long a file waits for its request.
const FileWait = 24 * time.Hour

// PutRequestFile keeps a file, waiting for its request.
func (s *Store) PutRequestFile(ctx context.Context, f RequestFile, content []byte) error {
	_, err := s.Pool.Exec(ctx, `insert into request_files (id, owner_id, name, content_type, size, sha256, content)
		values ($1, $2, $3, $4, $5, $6, $7)`, f.ID, f.OwnerID, f.Name, f.ContentType, f.Size, f.SHA256, content)
	return err
}

// GetRequestFile is a file's record, without its content.
func (s *Store) GetRequestFile(ctx context.Context, id string) (RequestFile, error) {
	rows, _ := s.Pool.Query(ctx, `select `+requestFileCols+` from request_files where id = $1`, id)
	f, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[RequestFile])
	return f, notFound(err)
}

// RequestFileContent is a file's content.
func (s *Store) RequestFileContent(ctx context.Context, id string) ([]byte, error) {
	var b []byte
	err := s.Pool.QueryRow(ctx, `select content from request_files where id = $1`, id).Scan(&b)
	return b, notFound(err)
}

// DropWaitingFiles deletes the files that waited longer than FileWait,
// and says how many of the owner's still wait.
func (s *Store) DropWaitingFiles(ctx context.Context, ownerID string) (int, error) {
	if _, err := s.Pool.Exec(ctx, `delete from request_files where request_id is null and created_at < now() - make_interval(secs => $1)`,
		FileWait.Seconds()); err != nil {
		return 0, err
	}
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from request_files where request_id is null and owner_id = $1`, ownerID).Scan(&n)
	return n, err
}

// AttachFiles gives a request the owner's waiting files among ids, and
// says how many it got.
func (s *Store) AttachFiles(ctx context.Context, db DB, requestID, ownerID string, ids []string) (int64, error) {
	tag, err := db.Exec(ctx, `update request_files set request_id = $1
		where id = any($2) and owner_id = $3 and request_id is null and created_at >= now() - make_interval(secs => $4)`,
		requestID, ids, ownerID, FileWait.Seconds())
	return tag.RowsAffected(), err
}

// FormImage is a picture uploaded for image fields (ADR-0040).
type FormImage struct {
	ID          string
	Name        string
	ContentType string
	Size        int64
	SHA256      string
	CreatedBy   *string
}

// PutFormImage keeps a picture for image fields.
func (s *Store) PutFormImage(ctx context.Context, img FormImage, content []byte) error {
	_, err := s.Pool.Exec(ctx, `insert into form_images (id, name, content_type, size, sha256, content, created_by)
		values ($1, $2, $3, $4, $5, $6, $7)`, img.ID, img.Name, img.ContentType, img.Size, img.SHA256, content, img.CreatedBy)
	return err
}

// GetFormImage is a picture and its content.
func (s *Store) GetFormImage(ctx context.Context, id string) (FormImage, []byte, error) {
	var img FormImage
	var content []byte
	err := s.Pool.QueryRow(ctx, `select id, name, content_type, size, sha256, created_by, content from form_images where id = $1`, id).
		Scan(&img.ID, &img.Name, &img.ContentType, &img.Size, &img.SHA256, &img.CreatedBy, &content)
	return img, content, notFound(err)
}

// MissingFormImages are those of ids that no picture has.
func (s *Store) MissingFormImages(ctx context.Context, ids []string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select id from unnest($1::text[]) as id where id not in (select id from form_images)`, ids)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
