package servicenow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory ServiceNow instance answering what Client calls: the
// Table API (encoded queries of =, !=, IN, ISEMPTY and ISNOTEMPTY joined by
// ^), OAuth's client credentials grant, and the IRE, which identifies a VM
// by its instance UUID, then by its managed object reference and vCenter,
// as the identification rules of cmdb_ci_vmware_instance do. Work notes
// added to a record are kept in its journal.
type Fake struct {
	Username, Password     string
	ClientID, ClientSecret string
	// DataSources are the discovery sources the IRE accepts.
	DataSources []string
	mu          sync.Mutex
	failing     map[string]int
	refusing    map[string]int
	tables      map[string][]Record
	journal     map[string][]string // sys_id -> work notes
	tokens      map[string]bool
	calls       []string
}

// NewFake is an empty instance, with credentials of both kinds and rvav as
// its one discovery source.
func NewFake() *Fake {
	return &Fake{Username: "rvav.integration", Password: "s3cret", ClientID: "rvav-client", ClientSecret: "client-s3cret",
		DataSources: []string{"rvav"}, tables: map[string][]Record{}, journal: map[string][]string{}, tokens: map[string]bool{}}
}

// Insert adds a record to a table and gives its sys_id.
func (f *Fake) Insert(table string, r Record) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.insert(table, r)
}

func (f *Fake) insert(table string, r Record) string {
	r = maps.Clone(r)
	if r["sys_id"] == "" {
		r["sys_id"] = newSysID()
	}
	r["sys_class_name"] = table
	r["sys_updated_on"] = time.Now().UTC().Format("2006-01-02 15:04:05")
	f.tables[table] = append(f.tables[table], r)
	return r["sys_id"]
}

// Rows are a table's records, copied.
func (f *Fake) Rows(table string) []Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Record, len(f.tables[table]))
	for i, r := range f.tables[table] {
		out[i] = maps.Clone(r)
	}
	return out
}

// Row is the record with this sys_id in a table, or nil.
func (f *Fake) Row(table, sysID string) Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.find(table, sysID); r != nil {
		return maps.Clone(r)
	}
	return nil
}

// WorkNotes are the work notes added to a record, oldest first.
func (f *Fake) WorkNotes(sysID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.journal[sysID])
}

// FailOn makes every call to a path with this prefix answer status, until
// it is called again with 0.
func (f *Fake) FailOn(prefix string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing == nil {
		f.failing = map[string]int{}
	}
	if status == 0 {
		delete(f.failing, prefix)
	} else {
		f.failing[prefix] = status
	}
}

// RefuseUpdates makes every update whose body holds this text answer
// status, as a data policy or a business rule refuses one, until it is
// called again with 0.
func (f *Fake) RefuseUpdates(text string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refusing == nil {
		f.refusing = map[string]int{}
	}
	if status == 0 {
		delete(f.refusing, text)
	} else {
		f.refusing[text] = status
	}
}

// Calls are the calls made so far, as "METHOD /path".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *Fake) find(table, sysID string) Record {
	for _, r := range f.tables[table] {
		if r["sys_id"] == sysID {
			return r
		}
	}
	return nil
}

func newSysID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	for prefix, status := range f.failing {
		if strings.HasPrefix(r.URL.Path, prefix) {
			fail(w, status, "Service unavailable", "the fake was told to fail")
			return
		}
	}
	if r.URL.Path == "/oauth_token.do" && r.Method == http.MethodPost {
		f.oauth(w, r)
		return
	}
	if !f.authorized(r) {
		fail(w, http.StatusUnauthorized, "User Not Authenticated", "Required to provide Auth information")
		return
	}
	switch {
	case r.URL.Path == "/api/now/identifyreconcile" && r.Method == http.MethodPost:
		f.identify(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/now/table/"):
		f.table(w, r, strings.Split(strings.TrimPrefix(r.URL.Path, "/api/now/table/"), "/"))
	default:
		fail(w, http.StatusBadRequest, "Requested URI does not represent any resource", r.URL.Path)
	}
}

func (f *Fake) oauth(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("client_id") != f.ClientID ||
		r.PostForm.Get("client_secret") != f.ClientSecret {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied", "error_description": "access_denied"})
		return
	}
	t := newSysID()
	f.tokens[t] = true
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": t, "token_type": "Bearer", "expires_in": 1799, "scope": "useraccount"})
}

func (f *Fake) authorized(r *http.Request) bool {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return f.tokens[t]
	}
	u, p, ok := r.BasicAuth()
	return ok && u == f.Username && p == f.Password
}

func fail(w http.ResponseWriter, status int, msg, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg, "detail": detail}, "status": "failure"})
}

func answer(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
}

func (f *Fake) table(w http.ResponseWriter, r *http.Request, parts []string) {
	table := parts[0]
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		q := r.URL.Query()
		var rows []Record
		for _, row := range f.tables[table] {
			if matches(row, q.Get("sysparm_query")) {
				rows = append(rows, row)
			}
		}
		off, _ := strconv.Atoi(q.Get("sysparm_offset"))
		lim, _ := strconv.Atoi(q.Get("sysparm_limit"))
		rows = rows[min(off, len(rows)):]
		if lim > 0 && lim < len(rows) {
			rows = rows[:lim]
		}
		out := make([]Record, len(rows))
		for i, row := range rows {
			out[i] = pick(row, q.Get("sysparm_fields"))
		}
		answer(w, http.StatusOK, out)
	case len(parts) == 1 && r.Method == http.MethodPost:
		var in Record
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			fail(w, http.StatusBadRequest, "Exception while reading request", "not JSON")
			return
		}
		id := f.insert(table, in)
		answer(w, http.StatusCreated, f.find(table, id))
	case len(parts) == 2 && (r.Method == http.MethodGet || r.Method == http.MethodPatch || r.Method == http.MethodPut):
		row := f.find(table, parts[1])
		if row == nil {
			fail(w, http.StatusNotFound, "No Record found", "Record doesn't exist or ACL restricts the record retrieval")
			return
		}
		if r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			for text, status := range f.refusing {
				if strings.Contains(string(body), text) {
					fail(w, status, "Operation Failed", "Data Policy Exception: the fake was told to refuse it")
					return
				}
			}
			var in map[string]any
			if json.Unmarshal(body, &in) != nil {
				fail(w, http.StatusBadRequest, "Exception while reading request", "not JSON")
				return
			}
			for k, v := range in {
				if k == "work_notes" || k == "comments" {
					if s := text(v); s != "" {
						f.journal[row["sys_id"]] = append(f.journal[row["sys_id"]], s)
					}
					continue
				}
				row[k] = text(v)
			}
			row["sys_updated_on"] = time.Now().UTC().Format("2006-01-02 15:04:05")
		}
		answer(w, http.StatusOK, pick(row, r.URL.Query().Get("sysparm_fields")))
	default:
		fail(w, http.StatusMethodNotAllowed, "Method not Supported", r.Method)
	}
}

func pick(row Record, fields string) Record {
	if fields == "" {
		return maps.Clone(row)
	}
	out := Record{}
	for _, f := range strings.Split(fields, ",") {
		out[f] = row[f]
	}
	return out
}

// matches says whether a row meets an encoded query's conditions.
func matches(row Record, query string) bool {
	if query == "" {
		return true
	}
	for _, c := range strings.Split(query, "^") {
		switch {
		case c == "" || strings.HasPrefix(c, "ORDERBY"):
		case strings.HasSuffix(c, "ISNOTEMPTY"):
			if row[strings.TrimSuffix(c, "ISNOTEMPTY")] == "" {
				return false
			}
		case strings.HasSuffix(c, "ISEMPTY"):
			if row[strings.TrimSuffix(c, "ISEMPTY")] != "" {
				return false
			}
		case strings.Contains(c, "!="):
			k, v, _ := strings.Cut(c, "!=")
			if row[k] == v {
				return false
			}
		case strings.Contains(c, "IN") && !strings.Contains(c, "="):
			k, v, _ := strings.Cut(c, "IN")
			if !slices.Contains(strings.Split(v, ","), row[k]) {
				return false
			}
		case strings.Contains(c, "="):
			k, v, _ := strings.Cut(c, "=")
			if row[k] != v {
				return false
			}
		}
	}
	return true
}

// identify is the IRE: one item of a class, matched on the class's
// identification rule; inserted with the data source as its discovery
// source when none matches.
func (f *Fake) identify(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("sysparm_data_source")
	if !slices.Contains(f.DataSources, src) {
		fail(w, http.StatusBadRequest, "Invalid data source", fmt.Sprintf("%q is not a discovery source", src))
		return
	}
	var in struct {
		Items []struct {
			ClassName string         `json:"className"`
			Values    map[string]any `json:"values"`
		} `json:"items"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Items) == 0 {
		fail(w, http.StatusBadRequest, "Invalid payload", "items are needed")
		return
	}
	type result struct {
		ClassName string           `json:"className"`
		Operation string           `json:"operation"`
		SysID     string           `json:"sysId"`
		Errors    []map[string]any `json:"errors"`
	}
	var out []result
	for _, it := range in.Items {
		v := Record{}
		for k, x := range it.Values {
			v[k] = text(x)
		}
		row := f.match(it.ClassName, v)
		switch {
		case it.ClassName == "" || (v["vm_instance_uuid"] == "" && (v["object_id"] == "" || v["vcenter_uuid"] == "")):
			out = append(out, result{ClassName: it.ClassName, Operation: "NO_CHANGE", Errors: []map[string]any{{
				"error": "MISSING_MATCHING_ATTRIBUTES", "message": "In payload no matching identifier rule found"}}})
		case row == nil:
			v["discovery_source"] = src
			id := f.insert(it.ClassName, v)
			out = append(out, result{ClassName: it.ClassName, Operation: "INSERT", SysID: id})
		default:
			op := "NO_CHANGE"
			for k, x := range v {
				if row[k] != x {
					row[k], op = x, "UPDATE"
				}
			}
			out = append(out, result{ClassName: it.ClassName, Operation: op, SysID: row["sys_id"]})
		}
	}
	answer(w, http.StatusOK, map[string]any{"items": out, "relations": []any{}})
}

func (f *Fake) match(class string, v Record) Record {
	rows := f.tables[class]
	if id := v["vm_instance_uuid"]; id != "" {
		for _, row := range rows {
			if row["vm_instance_uuid"] == id {
				return row
			}
		}
	}
	if v["object_id"] != "" && v["vcenter_uuid"] != "" {
		for _, row := range rows {
			if row["object_id"] == v["object_id"] && row["vcenter_uuid"] == v["vcenter_uuid"] {
				return row
			}
		}
	}
	return nil
}
