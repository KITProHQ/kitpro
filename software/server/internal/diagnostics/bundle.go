package diagnostics

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential|private[_-]?key|authorization|cookie|session|api[_-]?key)`)
var inlineSecret = regexp.MustCompile(`(?i)(authorization:\s*[^\r\n]+|cookie:\s*[^\r\n]+|bearer\s+[A-Za-z0-9._~+/=-]+|-----BEGIN [A-Z ]*PRIVATE KEY-----|(?:password|token|secret|api[_-]?key)\s*[=:]\s*\S+)`)

type Input struct {
	BundleID string
	Created  time.Time
	Sections map[string]any
	Logs     []string
}

type Result struct {
	BundleID string `json:"bundle_id"`
	Filename string `json:"filename"`
	Path     string `json:"-"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type Builder struct{ OutputRoot string }

func (b Builder) Build(input Input, forbidden []string) (Result, error) {
	if !validBundleID(input.BundleID) || input.Created.IsZero() {
		return Result{}, fmt.Errorf("invalid support bundle request")
	}
	root := b.OutputRoot
	if root == "" {
		root = "/var/lib/kitpro-api/support"
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return Result{}, fmt.Errorf("invalid support bundle root")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return Result{}, err
	}
	var archive bytes.Buffer
	var scan bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(input.Sections))
	for name := range input.Sections {
		if !safeSectionName(name) {
			return Result{}, fmt.Errorf("invalid support bundle section")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		redacted := Redact(input.Sections[name])
		value, err := json.MarshalIndent(redacted, "", "  ")
		if err != nil {
			return Result{}, err
		}
		value = append(value, '\n')
		scan.Write(value)
		if err = writeTarFile(tarWriter, name+".json", value, input.Created); err != nil {
			return Result{}, err
		}
	}
	if len(input.Logs) > 0 {
		lines := make([]string, len(input.Logs))
		for index, line := range input.Logs {
			lines[index] = inlineSecret.ReplaceAllString(line, "[REDACTED]")
		}
		logValue := []byte(strings.Join(lines, "\n") + "\n")
		scan.Write(logValue)
		if err := writeTarFile(tarWriter, "recent-logs.txt", logValue, input.Created); err != nil {
			return Result{}, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return Result{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return Result{}, err
	}
	payload := archive.Bytes()
	for _, secret := range forbidden {
		if secret != "" && bytes.Contains(scan.Bytes(), []byte(secret)) {
			return Result{}, fmt.Errorf("support bundle redaction failed")
		}
	}
	if bytes.Contains(scan.Bytes(), []byte("-----BEGIN PRIVATE KEY-----")) {
		return Result{}, fmt.Errorf("support bundle contains private-key marker")
	}
	filename := input.BundleID + ".tar.gz"
	path := filepath.Join(root, filename)
	file, err := os.OpenFile(path+".new", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Result{}, err
	}
	if _, err = file.Write(payload); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(path + ".new")
		return Result{}, err
	}
	if closeErr != nil {
		_ = os.Remove(path + ".new")
		return Result{}, closeErr
	}
	if err = os.Rename(path+".new", path); err != nil {
		_ = os.Remove(path + ".new")
		return Result{}, err
	}
	digest := sha256.Sum256(payload)
	return Result{BundleID: input.BundleID, Filename: filename, Path: path, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))}, nil
}

func Redact(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveKey.MatchString(key) {
				out[key] = "[REDACTED]"
			} else {
				out[key] = Redact(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = Redact(child)
		}
		return out
	case string:
		return inlineSecret.ReplaceAllString(typed, "[REDACTED]")
	default:
		return value
	}
}

func writeTarFile(writer *tar.Writer, name string, value []byte, created time.Time) error {
	header := &tar.Header{Name: name, Mode: 0600, Size: int64(len(value)), ModTime: created.UTC(), Typeflag: tar.TypeReg}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}

func validBundleID(value string) bool {
	if !strings.HasPrefix(value, "bundle-") || len(value) != 23 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "bundle-"))
	return err == nil
}

func safeSectionName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}
