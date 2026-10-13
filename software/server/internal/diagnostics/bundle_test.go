package diagnostics

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBundleRedactsSyntheticSecrets(t *testing.T) {
	const password = "known-password-value"
	const token = "known-cloudflare-token"
	const applicationSecret = "known-application-secret"
	result, err := (Builder{OutputRoot: t.TempDir()}).Build(Input{
		BundleID: "bundle-0123456789abcdef",
		Created:  time.Unix(1, 0),
		Sections: map[string]any{
			"system": map[string]any{"version": "alpha.14", "password": password, "nested": []any{map[string]any{"cloudflare_token": token}}},
		},
		Logs: []string{"healthy", "token=" + applicationSecret, "Authorization: Bearer " + token, "-----BEGIN PRIVATE KEY-----"},
	}, []string{password, token, applicationSecret})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(gzipReader)
	var content strings.Builder
	for {
		_, err = reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		value, _ := io.ReadAll(reader)
		content.Write(value)
	}
	for _, forbidden := range []string{password, token, applicationSecret, "BEGIN PRIVATE KEY"} {
		if strings.Contains(content.String(), forbidden) {
			t.Fatalf("bundle leaked %q: %s", forbidden, content.String())
		}
	}
	if !strings.Contains(content.String(), "[REDACTED]") {
		t.Fatal("bundle did not preserve redaction evidence")
	}
}

func TestBundleRejectsForbiddenValueAfterRedaction(t *testing.T) {
	_, err := (Builder{OutputRoot: t.TempDir()}).Build(Input{BundleID: "bundle-0123456789abcdef", Created: time.Now(), Sections: map[string]any{"system": map[string]any{"note": "opaque-value"}}}, []string{"opaque-value"})
	if err == nil {
		t.Fatal("forbidden value was accepted")
	}
}
