package vraimport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

func action(name, out, script string, inputs ...string) vra.VROAction {
	a := vra.VROAction{ID: "act-" + name, Name: name, Module: "com.corp.test", Version: "1.0.0", OutputType: out, Script: script}
	for _, in := range inputs {
		a.Inputs = append(a.Inputs, vra.VROParam{Name: in, Type: "string"})
	}
	return a
}

// TestMapAction: the vRO actions whose script makes one GET to a URL the
// import reads become REST sources, their inputs the URL reads parameters;
// the others say why not.
func TestMapAction(t *testing.T) {
	for _, c := range []struct {
		name   string
		a      vra.VROAction
		url    string
		params []string
		skip   string
	}{
		{name: "a literal URL, inputs added", a: action("getServices", "Array/string",
			`var req = new RESTRequest("https://cmdb.corp.example/api/services?env=" + environment + "&kind=" + kind);
return JSON.parse(req.execute().contentAsString);`, "environment", "kind", "unused"),
			url: "https://cmdb.corp.example/api/services?env=${params.environment}&kind=${params.kind}", params: []string{"environment", "kind"}},
		{name: "a host's URL, then the request's path", a: action("getVersions", "Array/Properties",
			`var host = RESTHostManager.createTransientHostFrom(RESTHostManager.createHost("dbaas"));
host.url = "https://dbaas.corp.example/";
return JSON.parse(host.createRequest("GET", "/versions?env=" + environment, null).execute().contentAsString);`, "environment"),
			url: "https://dbaas.corp.example/versions?env=${params.environment}", params: []string{"environment"}},
		{name: "one value", a: action("getOwner", "string", `return new RESTRequest("https://cmdb.corp.example/owner").execute().contentAsString;`),
			url: "https://cmdb.corp.example/owner", params: []string{}},
		{name: "a REST host by name", a: action("getVersions", "Array/string",
			`var host = RESTHostManager.getHost("dbaas"); return JSON.parse(host.createRequest("GET", "/versions?env=" + environment).execute().contentAsString);`,
			"environment"), skip: "REST host dbaas, whose URL the import doesn't know: add an entry under Administration › Form library with that host's URL and the path /versions?env=${params.environment}."},
		{name: "a POST", a: action("search", "Array/string", `var h = RESTHostManager.createHost("x"); h.url = "https://x.example";
return h.createRequest("POST", "/search", "{}").execute().contentAsString;`), skip: "It sends a POST request"},
		{name: "JavaScript only", a: action("legacy", "string", `return "unused";`), skip: "works its values out in JavaScript"},
		{name: "vCenter objects", a: action("getAllVMs", "Array/VC:VirtualMachine", `return VcPlugin.getAllVirtualMachines();`),
			skip: "It returns Array/VC:VirtualMachine"},
		{name: "a URL kept in a variable", a: action("built", "Array/string", `var url = "https://x.example/a";
return new RESTRequest(url + "?q=" + q).execute().contentAsString;`, "q"), skip: "built in steps the import can't follow"},
		{name: "two calls", a: action("twice", "Array/string", `var h = RESTHostManager.createHost("x"); h.url = "https://x.example";
h.createRequest("GET", "/a").execute(); return h.createRequest("GET", "/b").execute().contentAsString;`), skip: "several REST calls"},
		{name: "the host from an input", a: action("anyHost", "Array/string", `return new RESTRequest("https://" + host + "/x").execute().contentAsString;`, "host"),
			skip: "host comes from an input"},
		{name: "no input of the action", a: action("global", "Array/string", `return new RESTRequest("https://x.example/a?env=" + other).execute().contentAsString;`),
			skip: "reads other, which is no input of the action"},
	} {
		li := mapAction(c.a)
		if c.skip != "" {
			if li.Action != ActionSkip || !slices.ContainsFunc(li.Notes, func(n Note) bool { return n.Level == Unmapped && strings.Contains(n.Text, c.skip) }) {
				t.Errorf("%s: %s, notes %+v", c.name, li.Action, li.Notes)
			}
			continue
		}
		if li.Action == ActionSkip || li.URL != c.url || !slices.Equal(li.Parameters, c.params) || li.Entry["params"].(map[string]any)["url"] != c.url {
			t.Errorf("%s: %s %q %v, notes %+v", c.name, li.Action, li.URL, li.Parameters, li.Notes)
		}
	}
	li := mapAction(action("getServices", "Array/Properties", `return new RESTRequest("https://x.example/s?env=" + environment).execute();`, "environment", "unused"))
	b, _ := json.Marshal(li.Entry)
	if string(b) != `{"description":"vRO's action com.corp.test/getServices, version 1.0.0, imported from vRA.","kind":"rest","name":"Get services",`+
		`"parameters":[{"label":"Environment","name":"environment","required":true}],"params":{"items":"$","label":"$.label",`+
		`"url":"https://x.example/s?env=${params.environment}","value":"$.value"},"search":false,"use":"source"}` {
		t.Fatalf("entry = %s", b)
	}
	if !slices.ContainsFunc(li.Notes, func(n Note) bool { return n.Level == Info && strings.Contains(n.Text, "doesn't read unused") }) ||
		!slices.ContainsFunc(li.Notes, func(n Note) bool {
			return n.Level == Warning && strings.Contains(n.Text, "check the entry's JSONPaths")
		}) {
		t.Fatalf("notes = %+v", li.Notes)
	}
}

// libraryEstate is the sample estate, its getVersions action calling a URL
// the import reads, and an action for its Field types form's catalog.
func libraryEstate() vra.Estate {
	e := vra.SampleEstate(time.Now())
	e.Actions[0].Script = `var host = RESTHostManager.createTransientHostFrom(RESTHostManager.createHost("dbaas"));
host.url = "https://dbaas.corp.example";
return JSON.parse(host.createRequest("GET", "/versions?env=" + environment).execute().contentAsString);`
	e.Actions = append(e.Actions, action("search", "Array/string",
		`return JSON.parse(new RESTRequest("https://cmdb.corp.example/search?q=" + query).execute().contentAsString);`, "query"))
	e.Actions[len(e.Actions)-1].Module = "com.corp.cmdb"
	bp, form := vra.SampleFieldTypes()
	e.Blueprints, e.Forms = append(e.Blueprints, bp), append(e.Forms, form)
	return e
}

// TestPlanFormLibrary: an entry is made of each action forms use that can
// be one, rvav's of the same key left as it is; the fields name it.
func TestPlanFormLibrary(t *testing.T) {
	e := libraryEstate()
	byKey := func(list []LibraryImport) map[string]LibraryImport {
		out := map[string]LibraryImport{}
		for _, li := range list {
			out[li.Key] = li
		}
		return out
	}
	got := byKey(PlanFormLibrary(&e, Rvav{}))
	if v := got["getVersions"]; v.Action != ActionCreate || v.Version != 1 || v.VROAction != "com.corp.db/getVersions" ||
		v.URL != "https://dbaas.corp.example/versions?env=${params.environment}" {
		t.Fatalf("getVersions = %+v", v)
	}
	if u := got["getUsers"]; u.Action != ActionSkip || u.Notes[0].Text != "vRO has no such action in what the import read." {
		t.Fatalf("getUsers = %+v", u)
	}

	same := RvavEntry{Version: 2, Use: "source", Kind: "rest", Parameters: []string{"environment"}, Params: map[string]any{
		"url": "https://dbaas.corp.example/versions?env=${params.environment}", "items": "$", "value": "$"}}
	other := same
	other.Params = map[string]any{"url": "https://dbaas.corp.example/v2/versions"}
	for _, c := range []struct {
		have    RvavEntry
		action  string
		version int
	}{{same, ActionUnchanged, 2}, {other, ActionDiffers, 2}} {
		v := byKey(PlanFormLibrary(&e, Rvav{FormLibrary: map[string]RvavEntry{"getVersions": c.have}}))["getVersions"]
		if v.Action != c.action || v.Version != c.version {
			t.Errorf("rvav has %+v: %s %d", c.have, v.Action, v.Version)
		}
	}
	denied := byKey(PlanFormLibrary(&e, Rvav{RESTAllowed: func(string) bool { return false }}))["getVersions"]
	if denied.Action != ActionSkip || !strings.Contains(denied.Notes[len(denied.Notes)-1].Text, "rvav may not call dbaas.corp.example:") {
		t.Fatalf("a host not allowed = %+v", denied)
	}

	r := Rvav{Now: time.Now(), ProjectMap: map[string]string{}}
	st := ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}}
	plan := PlanImport(&e, r, st)
	if plan.Summary.FormLibraryCreate != 2 {
		t.Fatalf("form library to make = %d", plan.Summary.FormLibraryCreate)
	}
	item := func(id string) ItemImport {
		return plan.Items[slices.IndexFunc(plan.Items, func(x ItemImport) bool { return x.BlueprintID == id })]
	}
	note := func(it ItemImport, level, where, text string) bool {
		return slices.ContainsFunc(it.Notes, func(n Note) bool { return n.Level == level && n.Where == where && strings.Contains(n.Text, text) })
	}
	db := item("bp-db")
	field := db.Doc.Versions[0].UISchema["fields"].(map[string]any)["dbVersion"].(map[string]any)
	b, _ := json.Marshal(map[string]any{"vs": field["value_source"], "deps": field["depends_on"], "widget": field["widget"]})
	if string(b) != `{"deps":["$environment"],"vs":{"kind":"library","name":"getVersions","params":{"environment":"environment"},"version":1},"widget":"select"}` ||
		!note(db, Info, "form.dbVersion", "from form library entry getVersions (version 1), made of it") ||
		note(db, Unmapped, "form.dbVersion", "") {
		t.Fatalf("dbVersion = %s, notes %+v", b, db.Notes)
	}
	types := item("bp-types")
	if !note(types, Unmapped, "form.catalog", "The import made form library entry search of it, but vRA's form gives the action nothing for query") ||
		!note(types, Unmapped, "form.owners", "The import makes no form library entry of it: vRO has no such action") {
		t.Fatalf("notes = %+v", types.Notes)
	}

	// Without an entry, the field's note says why.
	e = vra.SampleEstate(time.Now())
	plan = PlanImport(&e, r, st)
	if db = item("bp-db"); !note(db, Unmapped, "form.dbVersion", "no form library entry of it: it calls vRO's REST host dbaas") {
		t.Fatalf("notes = %+v", db.Notes)
	}
}

// TestLibraryDefault: a field whose default comes from an action giving
// one value takes it from the entry; one given a list doesn't.
func TestLibraryDefault(t *testing.T) {
	form := &vra.Form{Form: `{"layout":{"pages":[{"id":"p","title":"","sections":[{"id":"s","fields":[
{"id":"owner","display":"textField"},{"id":"site","display":"textField"}]}]}]},
"schema":{"owner":{"label":"Owner","default":{"id":"com.corp.cmdb/getOwner","type":"scriptAction","parameters":[{"app":"app"}]}},
"site":{"label":"Site","default":{"id":"com.corp.cmdb/getSites","type":"scriptAction","parameters":[]}}}}`}
	props := map[string]any{"owner": map[string]any{"type": "string"}, "site": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"}}
	fields := map[string]any{"owner": map[string]any{"widget": "text"}, "site": map[string]any{"widget": "text"}}
	initial := map[string]any{"owner": "nobody"}
	ui := map[string]any{"fields": fields, "layout": map[string]any{"kind": "page", "sections": []map[string]any{
		{"key": "inputs", "title": "Request", "columns": 2, "fields": []string{"owner", "site"}}}}}
	lib := map[string]LibraryImport{
		"com.corp.cmdb/getOwner": {Key: "getOwner", Action: ActionCreate, Version: 1, single: true, Parameters: []string{"app"}, inputs: []string{"app"}},
		"com.corp.cmdb/getSites": {Key: "getSites", Action: ActionUnchanged, Version: 4, Parameters: []string{}},
	}
	var notes []string
	required := []string{}
	applyForm(form, props, fields, &required, initial, ui, lib, func(level, where, text string) {
		notes = append(notes, level+" "+where+": "+text)
	})
	b, _ := json.Marshal(fields["owner"].(map[string]any)["value_source"])
	if string(b) != `{"kind":"library","name":"getOwner","params":{"app":"inputs.app"},"version":1}` || initial["owner"] != nil {
		t.Fatalf("owner's source = %s, initial %v", b, initial)
	}
	if fields["site"].(map[string]any)["value_source"] != nil ||
		!slices.ContainsFunc(notes, func(n string) bool {
			return strings.HasPrefix(n, "unmapped form.site:") && strings.Contains(n, "getSites, made of it, gives a list")
		}) ||
		!slices.ContainsFunc(notes, func(n string) bool {
			return strings.HasPrefix(n, "info form.owner:") && strings.Contains(n, "Its default, which the requester can change")
		}) {
		t.Fatalf("notes = %v", notes)
	}
}
