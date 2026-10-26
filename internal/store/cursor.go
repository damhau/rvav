package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// Cursor is a keyset pagination position: the sort value and ID of the last
// row of the previous page. It is opaque to API clients.
type Cursor struct {
	Value string `json:"v"`
	ID    string `json:"id"`
}

var ErrBadCursor = errors.New("invalid cursor")

func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (*Cursor, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrBadCursor
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID == "" {
		return nil, ErrBadCursor
	}
	return &c, nil
}

// Page is a page of rows plus the cursor for the next one (nil on the last page).
type Page[T any] struct {
	Items []T
	Next  *Cursor
}

// paginate trims a result fetched with limit+1 rows and computes the next cursor.
func paginate[T any](rows []T, limit int, key func(T) Cursor) Page[T] {
	if len(rows) <= limit {
		return Page[T]{Items: rows}
	}
	rows = rows[:limit]
	next := key(rows[len(rows)-1])
	return Page[T]{Items: rows, Next: &next}
}
