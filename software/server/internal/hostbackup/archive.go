package hostbackup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	controlbackup "github.com/kitpro/kitpro/software/server/internal/backup"
)

const (
	Format        = "kitpro-host-state"
	FormatVersion = 1
	archiveRoot   = "kitpro-host-state"
	maxEntries    = 4096
	maxFileBytes  = int64(64 << 20)
	maxTotalBytes = int64(512 << 20)
)

var ErrInterrupted = errors.New("simulated host-state restore interruption")

type Entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	Secret bool   `json:"secret"`
}

type Manifest struct {
	Format                          string    `json:"format"`
	FormatVersion                   int       `json:"format_version"`
	CreatedAt                       time.Time `json:"created_at"`
	HelperSchemaVersion             int       `json:"helper_schema_version"`
	CertificatePrivateKeysIncluded  bool      `json:"certificate_private_keys_included"`
	CloudflareCredentialsIncluded   bool      `json:"cloudflare_credentials_included"`
	RemoteCloudflareStateIncluded   bool      `json:"remote_cloudflare_resources_included"`
	GeneratedIngressRuntimeIncluded bool      `json:"generated_ingress_runtime_included"`
	Entries                         []Entry   `json:"entries"`
}

type CreateOptions struct {
	DB          *sql.DB
	Destination string
	Name        string
	StateRoot   string
	Now         time.Time
}

type Result struct {
	Path     string
	SHA256   string
	Manifest Manifest
}

type sourceFile struct {
	archive string
	path    string
	entry   Entry
}

func Create(ctx context.Context, options CreateOptions) (Result, error) {
	if options.DB == nil || filepath.Base(options.Name) != options.Name || !strings.HasSuffix(options.Name, ".kitpro-host-state.tar.gz") {
		return Result{}, fmt.Errorf("invalid host-state backup request")
	}
	stateRoot := cleanRoot(options.StateRoot)
	if stateRoot == "" {
		return Result{}, fmt.Errorf("invalid helper state root")
	}
	if err := os.MkdirAll(options.Destination, 0700); err != nil {
		return Result{}, err
	}
	if err := os.Chmod(options.Destination, 0700); err != nil {
		return Result{}, err
	}
	finalPath := filepath.Join(options.Destination, options.Name)
	if _, err := os.Stat(finalPath); err == nil {
		return Result{}, fmt.Errorf("backup already exists")
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	workspace, err := os.MkdirTemp(options.Destination, ".host-backup-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(workspace)
	databasePath, err := controlbackup.Vacuum(ctx, options.DB, workspace, "helper.db")
	if err != nil {
		return Result{}, err
	}
	files := []sourceFile{{archive: "helper.db", path: databasePath}}
	for _, source := range []struct {
		archive string
		path    string
	}{
		{"ingress/certificates", filepath.Join(stateRoot, "ingress", "certificates")},
		{"acme", filepath.Join(stateRoot, "acme")},
		{"providers/cloudflare", filepath.Join(stateRoot, "providers", "cloudflare")},
	} {
		found, walkErr := collect(source.archive, source.path)
		if walkErr != nil {
			return Result{}, walkErr
		}
		files = append(files, found...)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].archive < files[j].archive })
	var schema int
	if err = options.DB.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&schema); err != nil {
		return Result{}, err
	}
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	manifest := Manifest{Format: Format, FormatVersion: FormatVersion, CreatedAt: now, HelperSchemaVersion: schema, CertificatePrivateKeysIncluded: true, CloudflareCredentialsIncluded: true, RemoteCloudflareStateIncluded: false, GeneratedIngressRuntimeIncluded: false}
	for index := range files {
		entry, hashErr := inspectFile(files[index].path, files[index].archive, files[index].archive != "helper.db")
		if hashErr != nil {
			return Result{}, hashErr
		}
		files[index].entry = entry
		manifest.Entries = append(manifest.Entries, entry)
	}
	var total int64
	for _, entry := range manifest.Entries {
		total += entry.Size
		if total > maxTotalBytes {
			return Result{}, fmt.Errorf("host-state source is too large")
		}
	}
	temporary, err := os.OpenFile(filepath.Join(options.Destination, "."+options.Name+".partial"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Result{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = writeArchive(temporary, manifest, files); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Result{}, err
	}
	if err = os.Rename(temporaryPath, finalPath); err != nil {
		return Result{}, err
	}
	if err = syncDirectory(options.Destination); err != nil {
		return Result{}, err
	}
	digest, err := hashFile(finalPath)
	return Result{Path: finalPath, SHA256: digest, Manifest: manifest}, err
}

func collect(prefix, root string) ([]sourceFile, error) {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("invalid host-state source %s", prefix)
	}
	result := []sourceFile{}
	err = filepath.WalkDir(root, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || item.IsDir() {
			return nil
		}
		info, statErr := item.Info()
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("host-state source contains unsupported file")
		}
		relative, _ := filepath.Rel(root, path)
		if !safeRelative(relative) {
			return fmt.Errorf("invalid host-state source path")
		}
		result = append(result, sourceFile{archive: filepath.ToSlash(filepath.Join(prefix, relative)), path: path})
		if len(result) > maxEntries {
			return fmt.Errorf("host-state source has too many files")
		}
		return nil
	})
	return result, err
}

func inspectFile(path, archive string, secret bool) (Entry, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return Entry{}, fmt.Errorf("invalid host-state source file")
	}
	digest, err := hashFile(path)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Path: archive, SHA256: digest, Size: info.Size(), Mode: 0600, Secret: secret}, nil
}

func writeArchive(destination io.Writer, manifest Manifest, files []sourceFile) error {
	gzipWriter := gzip.NewWriter(destination)
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	tarWriter := tar.NewWriter(gzipWriter)
	encoded, err := json.Marshal(manifest)
	if err == nil {
		err = writeBytes(tarWriter, archiveRoot+"/manifest.json", encoded, 0600)
	}
	for _, source := range files {
		if err != nil {
			break
		}
		file, openErr := os.Open(source.path)
		if openErr != nil {
			err = openErr
			break
		}
		header := &tar.Header{Name: archiveRoot + "/" + source.archive, Mode: 0600, Size: source.entry.Size, ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
		if err = tarWriter.WriteHeader(header); err == nil {
			hash := sha256.New()
			var written int64
			written, err = io.Copy(io.MultiWriter(tarWriter, hash), io.LimitReader(file, source.entry.Size+1))
			if err == nil && (written != source.entry.Size || hex.EncodeToString(hash.Sum(nil)) != source.entry.SHA256) {
				err = fmt.Errorf("host-state source changed during backup")
			}
		}
		_ = file.Close()
	}
	if closeErr := tarWriter.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gzipWriter.Close(); err == nil {
		err = closeErr
	}
	return err
}

func writeBytes(writer *tar.Writer, name string, value []byte, mode int64) error {
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(value)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func cleanRoot(value string) string {
	if value == "" {
		value = "/var/lib/kitpro-helper"
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return ""
	}
	return value
}

func safeRelative(value string) bool {
	return value != "" && value != "." && filepath.Clean(value) == value && !filepath.IsAbs(value) && value != ".." && !strings.HasPrefix(value, ".."+string(filepath.Separator))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
