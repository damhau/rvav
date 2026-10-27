package awxcatalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/integrations/awx"
)

func ptr(n int) *int { return &n }

var survey = []awx.SurveyQuestion{
	{Variable: "monitoring_agent", Question: "Agent", Type: "multiplechoice", Choices: awx.Choices{"node_exporter", "telegraf"},
		Default: "node_exporter", Required: true},
	{Variable: "agent_version", Question: "Version", Type: "text", Default: "1.8.2", Required: true, Max: ptr(20)},
	{Variable: "listen_port", Question: "Port", Description: "The port the agent listens on.", Type: "integer",
		Min: ptr(1024), Max: ptr(65535), Default: float64(9100), Required: true},
	{Variable: "collectors", Question: "Collectors", Type: "multiselect", Choices: awx.Choices{"cpu", "disk", "net"}, Default: "cpu\ndisk"},
	{Variable: "token", Question: "Token", Type: "password"},
	{Variable: "bad name", Question: "Bad", Type: "text"},
}

func TestForm(t *testing.T) {
	schema, ui, initial, warnings := Form("Install monitoring agent", survey)
	b, _ := json.Marshal(schema)
	got := string(b)
	for _, want := range []string{`"required":["monitoring_agent","agent_version","listen_port"]`,
		`"monitoring_agent":{"enum":["node_exporter","telegraf"],"type":"string"}`, `"agent_version":{"maxLength":20,"type":"string"}`,
		`"listen_port":{"maximum":65535,"minimum":1024,"type":"integer"}`,
		`"collectors":{"items":{"enum":["cpu","disk","net"],"type":"string"},"type":"array","uniqueItems":true}`,
		`"additionalProperties":false`} {
		if !strings.Contains(got, want) {
			t.Errorf("schema has no %s: %s", want, got)
		}
	}
	if strings.Contains(got, "token") || strings.Contains(got, "bad name") {
		t.Errorf("password or invalid variable in the schema: %s", got)
	}
	fields := ui["fields"].(map[string]any)
	if w := fields["monitoring_agent"].(map[string]any)["widget"]; w != "segmented" {
		t.Errorf("two choices widget = %v", w)
	}
	if f := fields["listen_port"].(map[string]any); f["widget"] != "number" || f["help"] != "The port the agent listens on." || f["label"] != "Port" {
		t.Errorf("port field = %v", f)
	}
	if w := fields["collectors"].(map[string]any)["widget"]; w != "checklist" {
		t.Errorf("multiselect widget = %v", w)
	}
	order := ui["layout"].(map[string]any)["sections"].([]map[string]any)[0]["fields"].([]string)
	if !slices.Equal(order, []string{"monitoring_agent", "agent_version", "listen_port", "collectors"}) {
		t.Errorf("order = %v", order)
	}
	if initial["listen_port"] != float64(9100) || initial["monitoring_agent"] != "node_exporter" ||
		!slices.Equal(initial["collectors"].([]string), []string{"cpu", "disk"}) {
		t.Errorf("initial = %v", initial)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "passwords") || !strings.Contains(warnings[1], `"bad name"`) {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestDiff(t *testing.T) {
	if d := Diff(survey, survey); len(d) != 0 {
		t.Fatalf("same survey = %v", d)
	}
	cur := slices.Clone(survey)
	cur[2].Max, cur[2].Required = ptr(32767), false
	cur[0].Choices = awx.Choices{"node_exporter", "telegraf", "otel"}
	cur = append(cur[:1], cur[2:]...) // agent_version removed
	cur = append(cur, awx.SurveyQuestion{Variable: "scrape_interval", Type: "integer"})
	got := strings.Join(Diff(survey, cur), "\n")
	want := strings.Join([]string{
		"agent_version removed",
		"monitoring_agent: choices node_exporter, telegraf → node_exporter, telegraf, otel",
		"listen_port: required yes → no",
		"listen_port: maximum 65535 → 32767",
		"scrape_interval added (integer)",
	}, "\n")
	if got != want {
		t.Fatalf("diff =\n%s\nwant\n%s", got, want)
	}
	swapped := []awx.SurveyQuestion{survey[1], survey[0]}
	if d := Diff(survey[:2], swapped); !slices.Equal(d, []string{"questions reordered"}) {
		t.Fatalf("reordered = %v", d)
	}
}
