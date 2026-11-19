package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLibraryRefs(t *testing.T) {
	ui := json.RawMessage(`{"fields":{
		"a":{"widget":"select","value_source":{"kind":"library","name":"dbVersions","version":2,"params":{"env":"inputs.environment"}}},
		"b":{"widget":"select","value_source":{"kind":"library","name":"dbVersions","version":2}},
		"c":{"widget":"select","value_source":{"kind":"rest","params":{"url":"https://x"}}},
		"d":{"widget":"text"}},
		"validations":[{"key":"cc","kind":"library","name":"costCentreOpen","version":1},{"key":"size","kind":"cel","params":{"check":"true"}},
			{"key":"old","kind":"library","name":"dbVersions","version":1}]}`)
	want := []LibraryRef{{Key: "costCentreOpen", Version: 1}, {Key: "dbVersions", Version: 1}, {Key: "dbVersions", Version: 2}}
	if got := LibraryRefs(ui); !reflect.DeepEqual(got, want) {
		t.Fatalf("refs = %v", got)
	}
	if got := LibraryRefs(json.RawMessage(`{"fields":[]}`)); got != nil {
		t.Fatalf("a schema that isn't one = %v", got)
	}
}
