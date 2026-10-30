package hostadmin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrPermission = errors.New("journal access denied")
var cursorPattern = regexp.MustCompile(`^[a-zA-Z0-9;:=_.+-]{1,512}$`)

const MaxJournalPage = 100
const maxJournalScan = 500
const maxJournalOutput = 4 * 1024 * 1024

type JournalQuery struct {
	Unit     string
	Priority *int
	Since    time.Time
	Until    time.Time
	Search   string
	Cursor   string
	Limit    int
}
type JournalEntry struct {
	Cursor     string `json:"cursor"`
	Timestamp  string `json:"timestamp"`
	Unit       string `json:"unit,omitempty"`
	Priority   int    `json:"priority"`
	Identifier string `json:"identifier,omitempty"`
	Message    string `json:"message"`
}
type JournalPage struct {
	Entries    []JournalEntry `json:"entries"`
	NextCursor string         `json:"next_cursor,omitempty"`
}
type JournalRunner interface {
	Run(context.Context, []string) ([]byte, error)
}
type JournalProvider struct{ Runner JournalRunner }

func NewJournalProvider() JournalProvider { return JournalProvider{Runner: fixedJournalctl{}} }
func (q JournalQuery) Validate() error {
	if q.Limit < 1 || q.Limit > MaxJournalPage || len(q.Search) > 100 || !utf8.ValidString(q.Search) || strings.ContainsAny(q.Search, "\x00\r\n") {
		return ErrInvalid
	}
	if q.Unit != "" && !ValidUnit(q.Unit) {
		return ErrInvalid
	}
	if q.Priority != nil && (*q.Priority < 0 || *q.Priority > 7) {
		return ErrInvalid
	}
	if q.Cursor != "" && !cursorPattern.MatchString(q.Cursor) {
		return ErrInvalid
	}
	if q.Since.IsZero() || q.Until.IsZero() || q.Until.Before(q.Since) || q.Until.Sub(q.Since) > 30*24*time.Hour || q.Until.After(time.Now().Add(time.Minute)) {
		return ErrInvalid
	}
	return nil
}

type limitedBuffer struct {
	bytes.Buffer
	Max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.Max {
		return 0, errors.New("journal response limit exceeded")
	}
	return b.Buffer.Write(p)
}

type fixedJournalctl struct{}

func (fixedJournalctl) Run(ctx context.Context, args []string) ([]byte, error) {
	if _, err := os.Stat("/usr/bin/journalctl"); err != nil {
		return nil, ErrUnsupported
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/journalctl", args...)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "TZ=UTC", "SYSTEMD_PAGER=cat"}
	out := &limitedBuffer{Max: maxJournalOutput}
	errOut := &limitedBuffer{Max: 2048}
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(errOut.String()), "permission") || strings.Contains(strings.ToLower(errOut.String()), "access denied") {
			return nil, ErrPermission
		}
		return nil, ErrUnavailable
	}
	if out.Len() == 0 && (strings.Contains(strings.ToLower(errOut.String()), "permission") || strings.Contains(strings.ToLower(errOut.String()), "access denied")) {
		return nil, ErrPermission
	}
	return out.Bytes(), nil
}
func (p JournalProvider) Query(ctx context.Context, q JournalQuery) (JournalPage, error) {
	if err := q.Validate(); err != nil {
		return JournalPage{}, err
	}
	scan := q.Limit * 5
	if scan < 50 {
		scan = 50
	}
	if scan > maxJournalScan {
		scan = maxJournalScan
	}
	args := []string{"--system", "--no-pager", "--quiet", "--output=json", "--reverse", "--lines=" + strconv.Itoa(scan), "--until=" + q.Until.UTC().Format("2006-01-02 15:04:05")}
	if q.Cursor == "" {
		args = append(args, "--since="+q.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	if q.Unit != "" {
		args = append(args, "--unit="+q.Unit)
	}
	if q.Priority != nil {
		args = append(args, "--priority=0.."+strconv.Itoa(*q.Priority))
	}
	if q.Cursor != "" {
		args = append(args, "--cursor="+q.Cursor)
	}
	raw, err := p.Runner.Run(ctx, args)
	if err != nil {
		return JournalPage{}, err
	}
	if len(raw) > maxJournalOutput {
		return JournalPage{}, ErrUnavailable
	}
	page := JournalPage{Entries: []JournalEntry{}}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	scanned := 0
	lastCursor := ""
	search := strings.ToLower(q.Search)
	for scanner.Scan() {
		scanned++
		if scanned > scan {
			return JournalPage{}, ErrUnavailable
		}
		entry, ok := parseJournalLine(scanner.Bytes())
		if !ok {
			continue
		}
		if entry.Cursor == q.Cursor {
			continue
		}
		lastCursor = entry.Cursor
		stamp, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil || stamp.Before(q.Since) || stamp.After(q.Until) {
			continue
		}
		if q.Unit != "" && entry.Unit != q.Unit {
			continue
		}
		if q.Priority != nil && entry.Priority > *q.Priority {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(entry.Message), search) {
			continue
		}
		page.Entries = append(page.Entries, entry)
		if len(page.Entries) == q.Limit {
			page.NextCursor = entry.Cursor
			break
		}
	}
	if scanner.Err() != nil {
		return JournalPage{}, ErrUnavailable
	}
	if page.NextCursor == "" && scanned >= scan {
		page.NextCursor = lastCursor
	}
	return page, nil
}
func safeField(fields map[string]json.RawMessage, key string, max int) string {
	var value string
	if json.Unmarshal(fields[key], &value) != nil || !utf8.ValidString(value) {
		return ""
	}
	if len(value) > max {
		value = value[:max]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
func parseJournalLine(line []byte) (JournalEntry, bool) {
	if len(line) > 64*1024 {
		return JournalEntry{}, false
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(line, &fields) != nil {
		return JournalEntry{}, false
	}
	cursor := safeField(fields, "__CURSOR", 512)
	stamp := safeField(fields, "__REALTIME_TIMESTAMP", 32)
	priorityText := safeField(fields, "PRIORITY", 2)
	if !cursorPattern.MatchString(cursor) {
		return JournalEntry{}, false
	}
	us, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || us < 0 {
		return JournalEntry{}, false
	}
	priority, err := strconv.Atoi(priorityText)
	if err != nil || priority < 0 || priority > 7 {
		return JournalEntry{}, false
	}
	unit := safeField(fields, "_SYSTEMD_UNIT", 200)
	if unit == "" {
		unit = safeField(fields, "UNIT", 200)
	}
	if unit == "" {
		unit = safeField(fields, "OBJECT_SYSTEMD_UNIT", 200)
	}
	if !ValidUnit(unit) {
		unit = ""
	}
	message := safeField(fields, "MESSAGE", 2048)
	return JournalEntry{Cursor: cursor, Timestamp: time.UnixMicro(us).UTC().Format(time.RFC3339Nano), Unit: unit, Priority: priority, Identifier: safeField(fields, "SYSLOG_IDENTIFIER", 100), Message: message}, true
}

var _ io.Writer = (*limitedBuffer)(nil)
