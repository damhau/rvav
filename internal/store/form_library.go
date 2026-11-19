package store

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// LibraryEntry is one version of an entry of the form library (ADR-0043):
// a value source or a validation, REST or CEL, that forms name by key and
// version. Params is a JSON object, Parameters a JSON list.
type LibraryEntry struct {
	Key         string          `db:"key"`
	Version     int             `db:"version"`
	Name        string          `db:"name"`
	Description string          `db:"description"`
	Use         string          `db:"use"`
	Kind        string          `db:"kind"`
	Params      json.RawMessage `db:"params"`
	Search      bool            `db:"search"`
	Parameters  json.RawMessage `db:"parameters"`
	Message     string          `db:"message"`
	CreatedAt   time.Time       `db:"created_at"`
}

// LibraryRef is a form's reference to a version of a library entry: a
// field's value source, or a validation.
type LibraryRef struct {
	Key     string
	Version int
}

// LibraryRefs reads the library entries a UI schema names, each once.
func LibraryRefs(uiSchema json.RawMessage) []LibraryRef {
	var ui struct {
		Fields map[string]struct {
			ValueSource *libraryRef `json:"value_source"`
		} `json:"fields"`
		Validations []libraryRef `json:"validations"`
	}
	if json.Unmarshal(uiSchema, &ui) != nil {
		return nil
	}
	var out []LibraryRef
	add := func(r libraryRef) {
		ref := LibraryRef{Key: r.Name, Version: r.Version}
		if r.Kind == "library" && r.Name != "" && !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	for _, f := range ui.Fields {
		if f.ValueSource != nil {
			add(*f.ValueSource)
		}
	}
	for _, v := range ui.Validations {
		add(v)
	}
	slices.SortFunc(out, func(a, b LibraryRef) int {
		return cmp.Or(strings.Compare(a.Key, b.Key), a.Version-b.Version)
	})
	return out
}

type libraryRef struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

const libraryCols = `key, version, name, description, use, kind, params, search, parameters, message, created_at`

// LibraryEntries lists every entry of the form library at its latest version.
func (s *Store) LibraryEntries(ctx context.Context) ([]LibraryEntry, error) {
	rows, _ := s.Pool.Query(ctx, `select distinct on (key) `+libraryCols+` from form_library order by key, version desc`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[LibraryEntry])
}

// GetLibraryEntry gives a library entry at a version, at its latest when
// version is 0.
func (s *Store) GetLibraryEntry(ctx context.Context, key string, version int) (LibraryEntry, error) {
	rows, _ := s.Pool.Query(ctx, `select `+libraryCols+` from form_library where key = $1 and ($2 = 0 or version = $2)
		order by version desc limit 1`, key, version)
	e, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LibraryEntry])
	return e, notFound(err)
}

// LibraryEntryVersions lists a library entry's version numbers, the latest
// last.
func (s *Store) LibraryEntryVersions(ctx context.Context, key string) ([]int, error) {
	rows, _ := s.Pool.Query(ctx, `select version from form_library where key = $1 order by version`, key)
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

// AddLibraryEntryVersion saves a library entry as its next version, its
// first when first, and gives its number: ErrInUse when first and the key
// exists, or when another save took the number; ErrNotFound when not first
// and it doesn't.
func (s *Store) AddLibraryEntryVersion(ctx context.Context, e LibraryEntry, first bool, by string) (int, error) {
	var version int
	err := s.Pool.QueryRow(ctx, `insert into form_library (key, version, name, description, use, kind, params, search, parameters,
			message, created_by)
		select $1, coalesce(max(version), 0) + 1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, '') from form_library where key = $1
		having (count(*) = 0) = $11 returning version`,
		e.Key, e.Name, e.Description, e.Use, e.Kind, e.Params, e.Search, e.Parameters, e.Message, by, first).Scan(&version)
	switch {
	case IsUniqueViolation(err), errors.Is(err, pgx.ErrNoRows) && first:
		return 0, ErrInUse
	case errors.Is(err, pgx.ErrNoRows):
		return 0, ErrNotFound
	}
	return version, err
}

// DeleteLibraryEntry deletes every version of a library entry, unless an
// item's version other than a draft names it: it gives their slugs then,
// and deletes nothing.
func (s *Store) DeleteLibraryEntry(ctx context.Context, key string) ([]string, error) {
	var users []string
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if err := LockLibraryEntry(ctx, tx, key); err != nil {
			return err
		}
		uses, err := libraryUses(ctx, tx, key)
		if err != nil {
			return err
		}
		for _, u := range uses {
			if u.State != "draft" && !slices.Contains(users, u.Slug) {
				users = append(users, u.Slug)
			}
		}
		if len(users) > 0 {
			return nil
		}
		tag, err := tx.Exec(ctx, `delete from form_library where key = $1`, key)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
	return users, err
}

// LockLibraryEntry holds a library entry until the transaction ends, so
// that it isn't deleted while a version naming it is published.
func LockLibraryEntry(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('rvav.form_library:' || $1))`, key)
	return err
}

// LibraryUse is an item's version naming a library entry at a version.
type LibraryUse struct {
	ItemID, Slug, Name, VersionID, Number, State string
	EntryVersion                                 int
}

// LibraryUses lists the items' versions naming a library entry, any
// version of it, by item slug then version.
func (s *Store) LibraryUses(ctx context.Context, key string) ([]LibraryUse, error) {
	return libraryUses(ctx, s.Pool, key)
}

func libraryUses(ctx context.Context, db DB, key string) ([]LibraryUse, error) {
	rows, err := db.Query(ctx, `select i.id, i.slug, i.name, v.id, v.number, v.state, v.ui_schema
		from catalog_item_versions v join catalog_items i on i.id = v.item_id
		where jsonb_path_exists(v.ui_schema, '$.fields.*.value_source ? (@.kind == "library" && @.name == $key)', jsonb_build_object('key', $1::text))
			or jsonb_path_exists(v.ui_schema, '$.validations[*] ? (@.kind == "library" && @.name == $key)', jsonb_build_object('key', $1::text))
		order by i.slug, v.created_at, v.id`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LibraryUse
	for rows.Next() {
		var u LibraryUse
		var ui json.RawMessage
		if err := rows.Scan(&u.ItemID, &u.Slug, &u.Name, &u.VersionID, &u.Number, &u.State, &ui); err != nil {
			return nil, err
		}
		for _, r := range LibraryRefs(ui) {
			if r.Key == key {
				u.EntryVersion = r.Version
				out = append(out, u)
			}
		}
	}
	return out, rows.Err()
}

// LibraryGoneError is a library entry a version names that was deleted
// since it was checked.
type LibraryGoneError struct {
	Key     string
	Version int
}

func (e *LibraryGoneError) Error() string {
	return fmt.Sprintf("form library entry %s version %d is gone", e.Key, e.Version)
}

// librariesStay holds the library entries the UI schema names until the
// transaction ends, once it has seen each named version is there.
func librariesStay(ctx context.Context, tx pgx.Tx, uiSchema json.RawMessage) error {
	for _, r := range LibraryRefs(uiSchema) {
		if err := LockLibraryEntry(ctx, tx, r.Key); err != nil {
			return err
		}
		var there bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from form_library where key = $1 and version = $2)`,
			r.Key, r.Version).Scan(&there); err != nil {
			return err
		}
		if !there {
			return &LibraryGoneError{Key: r.Key, Version: r.Version}
		}
	}
	return nil
}
