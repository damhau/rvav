package auditlog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestFormatHeader: MSGID is the action cut at a dot, never in a word, and MSG
// starts with a byte order mark when it isn't all ASCII (RFC 5424).
func TestFormatHeader(t *testing.T) {
	at := time.Date(2026, 11, 22, 8, 30, 0, 0, time.UTC)
	for _, c := range []struct{ action, msgID string }{
		{"token.create", "token.create"},
		{"settings.audit_destination.test", "settings.audit_destination.test"},
		{"settings.audit_destination.create", "settings.audit_destination"},
		{"settings.notification_template.reset", "settings.notification_template"},
		{"a_single_word_action_longer_than_32_characters", "a_single_word_action_longer_than"},
	} {
		msg := string(Format(16, "rvav.corp.example", "rvav", Event{ID: "1", At: at, Action: c.action, Detail: json.RawMessage(`{}`)}))
		fields := strings.SplitN(msg, " ", 7)
		if len(fields) != 7 || fields[5] != c.msgID {
			t.Errorf("%s: %q", c.action, msg)
		}
		if body := fields[6]; !strings.HasPrefix(body, "- {") || !strings.Contains(body, `"action":"`+c.action+`"`) {
			t.Errorf("%s: MSG %q", c.action, body)
		}
	}
	e := Event{ID: "2", At: at, Action: "token.create", Detail: json.RawMessage(`{"name":"Léa's token"}`)}
	if msg := string(Format(16, "rvav.corp.example", "rvav", e)); !strings.Contains(msg, " - \ufeff{") {
		t.Errorf("not all ASCII, without a byte order mark: %q", msg)
	}
}
