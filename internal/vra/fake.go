package vra

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FakeServer answers the vRA API calls the reader makes, from an Estate,
// as vRA does: sign-in with a user or a refresh token, the access token on
// every call, pages by $top and $skip or by page and size, a template's
// content and versions only from their own calls.
type FakeServer struct {
	Username, Password, Domain string
	RefreshToken               string

	mu     sync.Mutex
	estate Estate
	tokens map[string]bool
	seq    int
}

// FakeRefreshToken is what the fake accepts and gives.
const FakeRefreshToken = "fake-vra-refresh-token"

func NewFakeServer(e Estate) *FakeServer {
	return &FakeServer{Username: "rvav-importer", Password: "vra", Domain: "corp.example", RefreshToken: FakeRefreshToken,
		estate: e, tokens: map[string]bool{}}
}

// Expire makes every access token given so far invalid, as when they time
// out.
func (f *FakeServer) Expire() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = map[string]bool{}
}

// Start serves on addr until ctx is done and returns its URL.
func (f *FakeServer) Start(ctx context.Context, addr string) (string, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	go func() { _ = srv.Serve(l) }()
	return "http://" + l.Addr().String(), nil
}

func (f *FakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/readyz":
		w.WriteHeader(http.StatusOK)
		return
	case r.Method == http.MethodPost && p == "/csp/gateway/am/api/login":
		var in struct{ Username, Password, Domain string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Username != f.Username || in.Password != f.Password || in.Domain != f.Domain {
			fail(w, http.StatusUnauthorized, "Invalid username or password")
			return
		}
		write(w, map[string]string{"refresh_token": f.RefreshToken})
		return
	case r.Method == http.MethodPost && p == "/iaas/api/login":
		var in struct {
			RefreshToken string `json:"refreshToken"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.RefreshToken != f.RefreshToken {
			fail(w, http.StatusBadRequest, "Invalid refresh token")
			return
		}
		f.seq++
		t := "fake-vra-access-" + strconv.Itoa(f.seq)
		f.tokens[t] = true
		write(w, map[string]string{"tokenType": "Bearer", "token": t})
		return
	}
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "the fake vRA only reads")
		return
	}
	if !f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] {
		fail(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	q := r.URL.Query()
	e := &f.estate
	switch parts := strings.Split(strings.Trim(p, "/"), "/"); {
	case p == "/iaas/api/projects":
		top(w, q, e.Projects)
	case p == "/iaas/api/image-profiles":
		top(w, q, e.Images)
	case p == "/iaas/api/flavor-profiles":
		top(w, q, e.Flavors)
	case p == "/blueprint/api/blueprints":
		list := make([]Blueprint, len(e.Blueprints))
		for i, b := range e.Blueprints {
			b.Content, b.Versions = "", nil // only from the template's own call
			list[i] = b
		}
		top(w, q, list)
	case len(parts) == 4 && strings.HasPrefix(p, "/blueprint/api/blueprints/"):
		if b, ok := f.blueprint(parts[3]); ok {
			b.Versions = nil
			write(w, b)
		} else {
			fail(w, http.StatusNotFound, "Not found")
		}
	case len(parts) == 5 && parts[4] == "versions":
		b, ok := f.blueprint(parts[3])
		if !ok {
			fail(w, http.StatusNotFound, "Not found")
			return
		}
		list := make([]BlueprintVersion, len(b.Versions))
		for i, v := range b.Versions {
			v.Content = ""
			list[i] = v
		}
		top(w, q, list)
	case len(parts) == 6 && parts[4] == "versions":
		b, _ := f.blueprint(parts[3])
		i := slices.IndexFunc(b.Versions, func(v BlueprintVersion) bool { return v.Version == parts[5] })
		if i < 0 {
			fail(w, http.StatusNotFound, "Not found")
			return
		}
		write(w, b.Versions[i])
	case p == "/form-service/api/forms/search":
		i := slices.IndexFunc(e.Forms, func(x Form) bool {
			return x.SourceID == q.Get("sourceId") && x.SourceType == q.Get("sourceType") && x.Type == q.Get("formType")
		})
		if i < 0 {
			fail(w, http.StatusNotFound, "Form not found")
			return
		}
		write(w, e.Forms[i])
	case p == "/catalog/api/admin/items":
		pages(w, q, e.CatalogItems)
	case p == "/catalog/api/admin/entitlements":
		out := []Entitlement{}
		for _, x := range e.Entitlements {
			if x.ProjectID == q.Get("projectId") {
				out = append(out, x)
			}
		}
		write(w, out)
	case p == "/policy/api/policies":
		pages(w, q, e.Policies)
	case p == "/deployment/api/deployments":
		list := e.Deployments
		if q.Get("expand") != "resources" {
			list = make([]Deployment, len(e.Deployments))
			for i, d := range e.Deployments {
				d.Resources = nil
				list[i] = d
			}
		}
		pages(w, q, list)
	case p == "/event-broker/api/subscriptions":
		pages(w, q, e.Subscriptions)
	case p == "/form-service/api/custom/resource-actions":
		pages(w, q, e.ResourceActions)
	case strings.HasPrefix(p, "/vco/api/"):
		f.vro(w, q, parts[2:])
	default:
		fail(w, http.StatusNotFound, "Not found")
	}
}

// vro answers vRO's calls: lists as links with attributes, paged by
// maxResult and startIndex.
func (f *FakeServer) vro(w http.ResponseWriter, q url.Values, parts []string) {
	e := &f.estate
	type attr struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	type link struct {
		Href       string `json:"href"`
		Rel        string `json:"rel"`
		Attributes []attr `json:"attributes"`
	}
	list := func(links []link) (part []link, total int) {
		return window(links, intParam(q, "startIndex", 0), max(intParam(q, "maxResult", 20), 1)), len(links)
	}
	params := func(ps []VROParam) []map[string]string {
		out := []map[string]string{}
		for _, p := range ps {
			out = append(out, map[string]string{"name": p.Name, "type": p.Type, "description": p.Description})
		}
		return out
	}
	switch {
	case len(parts) == 1 && parts[0] == "workflows":
		var links []link
		for _, x := range e.Workflows {
			links = append(links, link{Href: "/vco/api/workflows/" + x.ID + "/", Rel: "down", Attributes: []attr{{"id", x.ID},
				{"name", x.Name}, {"categoryName", x.Category}, {"description", x.Description}, {"version", x.Version}}})
		}
		part, total := list(links)
		write(w, map[string]any{"link": part, "total": total, "start": intParam(q, "startIndex", 0)})
	case len(parts) >= 2 && parts[0] == "workflows":
		i := slices.IndexFunc(e.Workflows, func(x VROWorkflow) bool { return x.ID == parts[1] })
		if i < 0 {
			fail(w, http.StatusNotFound, "Workflow not found")
			return
		}
		x := e.Workflows[i]
		switch {
		case len(parts) == 2:
			write(w, map[string]any{"id": x.ID, "name": x.Name, "version": x.Version, "description": x.Description,
				"input-parameters": params(x.Inputs), "output-parameters": []any{}})
		case parts[2] == "content":
			_, _ = w.Write([]byte(x.Content))
		case parts[2] == "executions":
			var links []link
			for n := range x.Runs {
				if x.LastRun == nil {
					break
				}
				t := x.LastRun.AddDate(0, 0, -n)
				id := x.ID + "-run-" + strconv.Itoa(n)
				links = append(links, link{Href: "/vco/api/workflows/" + x.ID + "/executions/" + id + "/", Rel: "down",
					Attributes: []attr{{"id", id}, {"state", "completed"}, {"startDate", t.Format("2006-01-02T15:04:05.000-07:00")}}})
			}
			part, total := list(links)
			write(w, map[string]any{"relations": map[string]any{"link": part, "total": total}})
		default:
			fail(w, http.StatusNotFound, "Not found")
		}
	case len(parts) == 1 && parts[0] == "actions":
		var links []link
		for _, a := range e.Actions {
			links = append(links, link{Href: "/vco/api/actions/" + a.ID + "/", Rel: "down", Attributes: []attr{{"id", a.ID},
				{"name", a.Name}, {"module", a.Module}, {"fqn", a.FQN()}, {"version", a.Version}}})
		}
		part, total := list(links)
		write(w, map[string]any{"link": part, "total": total})
	case len(parts) == 2 && parts[0] == "actions":
		i := slices.IndexFunc(e.Actions, func(a VROAction) bool { return a.ID == parts[1] })
		if i < 0 {
			fail(w, http.StatusNotFound, "Action not found")
			return
		}
		a := e.Actions[i]
		write(w, map[string]any{"id": a.ID, "name": a.Name, "module": a.Module, "version": a.Version, "output-type": a.OutputType,
			"input-parameters": params(a.Inputs), "script": a.Script})
	default:
		fail(w, http.StatusNotFound, "Not found")
	}
}

func (f *FakeServer) blueprint(id string) (Blueprint, bool) {
	id, _ = url.PathUnescape(id)
	i := slices.IndexFunc(f.estate.Blueprints, func(b Blueprint) bool { return b.ID == id })
	if i < 0 {
		return Blueprint{}, false
	}
	return f.estate.Blueprints[i], true
}

func write(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func fail(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	write(w, map[string]any{"message": msg, "statusCode": status})
}

func intParam(q url.Values, k string, def int) int {
	if n, err := strconv.Atoi(q.Get(k)); err == nil && n >= 0 {
		return n
	}
	return def
}

func top[T any](w http.ResponseWriter, q url.Values, all []T) {
	n, skip := intParam(q, "$top", 20), intParam(q, "$skip", 0)
	part := window(all, skip, n)
	write(w, map[string]any{"content": part, "totalElements": len(all), "numberOfElements": len(part)})
}

func pages[T any](w http.ResponseWriter, q url.Values, all []T) {
	size, page := intParam(q, "size", 20), intParam(q, "page", 0)
	if size == 0 {
		size = 20
	}
	part := window(all, page*size, size)
	totalPages := (len(all) + size - 1) / size
	write(w, map[string]any{"content": part, "totalElements": len(all), "totalPages": totalPages, "number": page,
		"last": page+1 >= totalPages, "numberOfElements": len(part)})
}

func window[T any](all []T, from, n int) []T {
	if from >= len(all) {
		return []T{}
	}
	return all[from:min(from+n, len(all))]
}
