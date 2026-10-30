package hostadmin

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeJournal struct {
	data []byte
	args []string
	err  error
}

func (f *fakeJournal) Run(_ context.Context, args []string) ([]byte, error) {
	f.args = args
	return f.data, f.err
}
func journalLine(cursor, unit, message string, priority int, at time.Time) []byte {
	b, _ := json.Marshal(map[string]any{"__CURSOR": cursor, "__REALTIME_TIMESTAMP": strconv.FormatInt(at.UnixMicro(), 10), "_SYSTEMD_UNIT": unit, "MESSAGE": message, "PRIORITY": strconv.Itoa(priority), "SYSLOG_IDENTIFIER": "test"})
	return append(b, '\n')
}
func TestJournalFilterCursorAndSensitiveInput(t *testing.T) {
	now := time.Now().UTC()
	f := &fakeJournal{}
	f.data = append(f.data, journalLine("s=one", "alpha.service", "secret-token=hidden", 3, now)...)
	f.data = append(f.data, journalLine("s=two", "beta.service", "needle", 4, now)...)
	f.data = append(f.data, journalLine("s=three", "alpha.service", "needle", 3, now)...)
	q := JournalQuery{Unit: "alpha.service", Search: "needle", Limit: 1, Since: now.Add(-time.Hour), Until: now.Add(time.Second), Cursor: "s=one"}
	page, e := (JournalProvider{Runner: f}).Query(context.Background(), q)
	if e != nil || len(page.Entries) != 1 || page.Entries[0].Cursor != "s=three" || page.Entries[0].Message != "needle" {
		t.Fatalf("page: %#v %v", page, e)
	}
	for _, arg := range f.args {
		if strings.HasPrefix(arg, "--since=") {
			t.Fatal("cursor query included mutually exclusive since argument")
		}
		if strings.Contains(arg, "secret-token") {
			t.Fatal("message entered command arguments")
		}
	}
}
func TestJournalInvalidAndBinary(t *testing.T) {
	now := time.Now().UTC()
	base := JournalQuery{Limit: 1, Since: now.Add(-time.Hour), Until: now}
	for _, q := range []JournalQuery{{Limit: 101, Since: base.Since, Until: base.Until}, {Limit: 1, Since: base.Since, Until: base.Until, Cursor: "--file=/etc/shadow"}, {Limit: 1, Since: base.Since, Until: base.Until, Unit: "../a.service"}, {Limit: 1, Since: now.Add(-31 * 24 * time.Hour), Until: now}} {
		if e := q.Validate(); !errors.Is(e, ErrInvalid) {
			t.Fatalf("accepted query: %#v", q)
		}
	}
	raw := []byte(`{"__CURSOR":"s=one","__REALTIME_TIMESTAMP":"` + strconv.FormatInt(now.UnixMicro(), 10) + `","PRIORITY":"3","MESSAGE":[255,0]}`)
	if entry, ok := parseJournalLine(raw); ok && entry.Message != "" {
		t.Fatal("binary message was rendered")
	}
}
func TestJournalBackendFailureDoesNotReturnRawError(t *testing.T) {
	now := time.Now().UTC()
	f := &fakeJournal{err: ErrPermission}
	_, e := (JournalProvider{Runner: f}).Query(context.Background(), JournalQuery{Limit: 1, Since: now.Add(-time.Hour), Until: now})
	if !errors.Is(e, ErrPermission) {
		t.Fatalf("error: %v", e)
	}
}
