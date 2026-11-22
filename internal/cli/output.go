package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func (a *app) jsonOut() bool { return a.output == "json" }

// printJSON writes v indented; raw JSON (the API's own bytes) keeps its
// fields and their order.
func (a *app) printJSON(v any) error {
	var b []byte
	var err error
	if raw, ok := v.(json.RawMessage); ok {
		var out bytes.Buffer
		err = json.Indent(&out, raw, "", "  ")
		b = out.Bytes()
	} else {
		b, err = json.MarshalIndent(v, "", "  ")
	}
	if err != nil {
		return err
	}
	_, err = a.Out.Write(append(b, '\n'))
	return err
}

type table struct{ w *tabwriter.Writer }

func newTable(out io.Writer, headers ...string) *table {
	t := &table{w: tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)}
	if len(headers) > 0 {
		t.row(headers...)
	}
	return t
}

func (t *table) row(cells ...string) {
	for i, c := range cells {
		cells[i] = cell(c)
	}
	fmt.Fprintln(t.w, strings.Join(cells, "\t"))
}

// cell is text from rvav as a terminal may show it: on one line, and with
// no control character, so that what a requester wrote can't move the
// cursor, clear a line or hide a link (ANSI and OSC 8 escapes).
func cell(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t', r == '\n', r == '\r':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r < 0xa0:
			return '�'
		}
		return r
	}, s)
}

// multiline is cell for text of several lines, such as an error.
func multiline(s string) string {
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = cell(p)
	}
	return strings.Join(parts, "\n")
}

func (t *table) flush() error { return t.w.Flush() }

// details writes label and value pairs, leaving out empty values.
func details(out io.Writer, pairs ...string) error {
	t := newTable(out)
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			t.row(pairs[i]+":", pairs[i+1])
		}
	}
	return t.flush()
}

func when(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptr[T any](v T) *T { return &v }

// optional is a flag's value, nil when it's empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func rawJSON(b []byte) json.RawMessage { return json.RawMessage(b) }

// rawItems are a page's items as the API wrote them.
func rawItems(body []byte) ([]json.RawMessage, error) {
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	err := json.Unmarshal(body, &page)
	return page.Items, err
}

// page is one page of a list: its items, typed and as the API wrote them,
// and the cursor of the next.
type page[T any] struct {
	items []T
	body  []byte
	next  *string
}

// listed is how many items a list shows: --limit, or every one with --all.
type listed struct {
	limit int
	all   bool
	what  string
}

func (l *listed) flags(c *cobra.Command, what string, limit int) {
	l.what = what
	c.Flags().IntVar(&l.limit, "limit", limit, "at most this many "+what)
	c.Flags().BoolVar(&l.all, "all", false, "every one of the "+what+", however many")
	c.MarkFlagsMutuallyExclusive("limit", "all")
}

func (l *listed) max() int {
	if l.all {
		return math.MaxInt
	}
	return l.limit
}

// collect reads pages until limit items or the last page, and says
// whether there are more.
func collect[T any](limit int, read func(cursor *string, n int) (page[T], error)) ([]T, []json.RawMessage, bool, error) {
	var items []T
	var raw []json.RawMessage
	var cursor *string
	for {
		n := min(limit-len(items), 200)
		p, err := read(cursor, n)
		if err != nil {
			return nil, nil, false, err
		}
		r, err := rawItems(p.body)
		if err != nil {
			return nil, nil, false, err
		}
		items, raw = append(items, p.items...), append(raw, r...)
		last := p.next == nil || *p.next == ""
		if last || len(items) >= limit {
			more := len(items) > limit || !last
			if len(items) > limit {
				items, raw = items[:limit], raw[:limit]
			}
			return items, raw, more, nil
		}
		cursor = p.next
	}
}

// more says on standard error that a list shows only the first of its
// items, whatever the output.
func (a *app) more(l listed, more bool) {
	if more {
		fmt.Fprintf(a.Err, "Showing the first %d %s; there are more. Give --limit or --all for more.\n", l.limit, l.what)
	}
}

// list prints a list's items as a JSON array, or a table; none is said
// on standard error, so that the output stays empty.
func (a *app) list(raw []json.RawMessage, none string, table func() error) error {
	if a.jsonOut() {
		if raw == nil {
			raw = []json.RawMessage{}
		}
		return a.printJSON(raw)
	}
	if len(raw) == 0 {
		_, err := fmt.Fprintln(a.Err, none)
		return err
	}
	return table()
}
