package appbackup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	FormatName    = "kitpro-application-backup"
	FormatVersion = 1
	archiveRoot   = "kitpro-backup"
)

type Manifest struct {
	Format            string            `json:"format"`
	FormatVersion     int               `json:"format_version"`
	KITProVersion     string            `json:"kitpro_version"`
	CreatedAt         time.Time         `json:"created_at"`
	ApplicationID     string            `json:"application_id"`
	InstallationID    string            `json:"installation_id"`
	ReleaseID         string            `json:"release_id"`
	RuntimeGeneration int               `json:"runtime_generation"`
	Strategy          string            `json:"strategy"`
	Components        []Component       `json:"components"`
	Storage           []Storage         `json:"storage"`
	ImportedStorage   []ImportedStorage `json:"imported_storage,omitempty"`
	Databases         []Database        `json:"databases,omitempty"`
	Secrets           []Secret          `json:"secrets,omitempty"`
	Checksum          ChecksumMetadata  `json:"checksum"`
}

type Component struct {
	ID          string   `json:"id"`
	ImageDigest string   `json:"image_digest"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

type Storage struct {
	Component   string `json:"component"`
	ID          string `json:"id"`
	Disposition string `json:"disposition"`
	ArchivePath string `json:"archive_path,omitempty"`
	OwnerUID    int    `json:"owner_uid,omitempty"`
	OwnerGID    int    `json:"owner_gid,omitempty"`
}

type ImportedStorage struct {
	Component       string `json:"component"`
	SlotID          string `json:"slot_id"`
	RootID          string `json:"root_id"`
	AccessMode      string `json:"access_mode"`
	Filesystem      string `json:"filesystem"`
	DeviceMajor     uint64 `json:"device_major"`
	DeviceMinor     uint64 `json:"device_minor"`
	Inode           uint64 `json:"inode"`
	NetworkBacked   bool   `json:"network_backed"`
	ContentIncluded bool   `json:"content_included"`
	ExclusionReason string `json:"exclusion_reason"`
}

type Database struct {
	Engine         string `json:"engine"`
	Component      string `json:"component"`
	StorageID      string `json:"storage_id"`
	RelativePath   string `json:"relative_path"`
	IntegrityCheck string `json:"integrity_check"`
}

type Secret struct {
	Component string `json:"component"`
	Name      string `json:"name"`
	Included  bool   `json:"included"`
}

type ChecksumMetadata struct {
	Algorithm string `json:"algorithm"`
	IndexPath string `json:"index_path"`
}

type Source struct {
	ArchivePath string
	HostPath    string
}

type InlineFile struct {
	ArchivePath string
	Contents    []byte
	Mode        fs.FileMode
}

type ChecksumEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   int64  `json:"mode"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
}

type ChecksumIndex struct {
	Algorithm string          `json:"algorithm"`
	Files     []ChecksumEntry `json:"files"`
}

type Limits struct {
	MaxEntries    int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

func DefaultLimits() Limits {
	return Limits{MaxEntries: 1_000_000, MaxFileBytes: 1 << 40, MaxTotalBytes: 4 << 40}
}

type ExtractOptions struct {
	Limits            Limits
	PreserveOwnership bool
}

type ExtractResult struct {
	Manifest  Manifest
	Checksums ChecksumIndex
}

// Measure validates the archive container and returns its declared extracted
// byte and entry counts without writing payload data.
func Measure(archivePath string, limits Limits) (int64, int, error) {
	if !filepath.IsAbs(archivePath) {
		return 0, 0, fmt.Errorf("archive path must be absolute")
	}
	if limits.MaxEntries <= 0 || limits.MaxFileBytes <= 0 || limits.MaxTotalBytes <= 0 {
		limits = DefaultLimits()
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, 0, fmt.Errorf("open compressed archive: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	var entries int
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return total, entries, nil
		}
		if err != nil {
			return 0, 0, fmt.Errorf("read archive: %w", err)
		}
		entries++
		if entries > limits.MaxEntries {
			return 0, 0, fmt.Errorf("archive entry limit exceeded")
		}
		name, err := validateArchiveName(header.Name)
		if err != nil || seen[name] {
			return 0, 0, fmt.Errorf("unsafe or duplicate archive entry %q", header.Name)
		}
		seen[name] = true
		if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return 0, 0, fmt.Errorf("archive links and special files are not supported")
		}
		if header.Size < 0 || header.Size > limits.MaxFileBytes || total > limits.MaxTotalBytes-header.Size {
			return 0, 0, fmt.Errorf("archive size limit exceeded")
		}
		total += header.Size
	}
}

func Create(destination string, manifest Manifest, sources []Source, inline []InlineFile) (string, error) {
	if err := ValidateManifest(manifest); err != nil {
		return "", err
	}
	if !filepath.IsAbs(destination) || strings.ContainsAny(filepath.Base(destination), "\r\n") {
		return "", fmt.Errorf("invalid archive destination")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return "", fmt.Errorf("create archive directory: %w", err)
	}
	partial := destination + ".partial"
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("create partial archive: %w", err)
	}
	complete := false
	defer func() {
		_ = f.Close()
		if !complete {
			_ = os.Remove(partial)
		}
	}()

	if err := writeArchive(f, manifest, sources, inline); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync archive: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close archive: %w", err)
	}
	if err := os.Rename(partial, destination); err != nil {
		return "", fmt.Errorf("publish archive: %w", err)
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		_ = os.Remove(destination)
		return "", err
	}
	complete = true
	return destination, nil
}

func writeArchive(out io.Writer, manifest Manifest, sources []Source, inline []InlineFile) error {
	gz := gzip.NewWriter(out)
	gz.Header.ModTime = time.Time{}
	gz.Header.OS = 255
	tw := tar.NewWriter(gz)
	checksums := make([]ChecksumEntry, 0)

	manifest.Checksum = ChecksumMetadata{Algorithm: "sha256", IndexPath: "checksums/sha256.json"}
	manifestBytes, err := canonicalJSON(manifest)
	if err != nil {
		return fmt.Errorf("encode backup manifest: %w", err)
	}
	inline = append(inline, InlineFile{ArchivePath: "manifest.json", Contents: manifestBytes, Mode: 0600})
	sort.Slice(inline, func(i, j int) bool { return inline[i].ArchivePath < inline[j].ArchivePath })
	seen := map[string]bool{}
	for _, item := range inline {
		name, err := canonicalPayloadPath(item.ArchivePath)
		if err != nil || seen[name] || name == "checksums/sha256.json" {
			return fmt.Errorf("invalid or duplicate inline archive path %q", item.ArchivePath)
		}
		seen[name] = true
		mode := item.Mode.Perm()
		if mode == 0 {
			mode = 0600
		}
		if err := writeBytes(tw, name, item.Contents, mode); err != nil {
			return err
		}
		checksums = append(checksums, checksumBytes(name, item.Contents, mode))
	}

	sort.Slice(sources, func(i, j int) bool { return sources[i].ArchivePath < sources[j].ArchivePath })
	for _, source := range sources {
		prefix, err := canonicalPayloadPath(source.ArchivePath)
		if err != nil || seen[prefix] {
			return fmt.Errorf("invalid or duplicate source archive path %q", source.ArchivePath)
		}
		info, err := os.Lstat(source.HostPath)
		if err != nil {
			return fmt.Errorf("inspect backup source: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup source must be a real directory")
		}
		if err := filepath.WalkDir(source.HostPath, func(hostPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(source.HostPath, hostPath)
			if err != nil {
				return err
			}
			name := prefix
			if rel != "." {
				name += "/" + filepath.ToSlash(rel)
			}
			if seen[name] {
				return fmt.Errorf("duplicate archive path %q", name)
			}
			seen[name] = true
			return writeHostEntry(tw, hostPath, name, entry, &checksums)
		}); err != nil {
			return fmt.Errorf("archive source %s: %w", source.ArchivePath, err)
		}
	}

	sort.Slice(checksums, func(i, j int) bool { return checksums[i].Path < checksums[j].Path })
	indexBytes, err := canonicalJSON(ChecksumIndex{Algorithm: "sha256", Files: checksums})
	if err != nil {
		return fmt.Errorf("encode checksum index: %w", err)
	}
	if err := writeBytes(tw, "checksums/sha256.json", indexBytes, 0600); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close compressed archive: %w", err)
	}
	return nil
}

func writeHostEntry(tw *tar.Writer, hostPath, archivePath string, entry fs.DirEntry, checksums *[]ChecksumEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("symbolic links are not supported: %s", archivePath)
	}
	if !mode.IsDir() && !mode.IsRegular() {
		return fmt.Errorf("special files are not supported: %s", archivePath)
	}
	header := &tar.Header{Name: archiveName(archivePath), Mode: int64(mode.Perm()), ModTime: info.ModTime(), Format: tar.FormatPAX}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		header.Uid = int(stat.Uid)
		header.Gid = int(stat.Gid)
	}
	if mode.IsDir() {
		header.Typeflag = tar.TypeDir
		header.Name += "/"
		return tw.WriteHeader(header)
	}
	header.Typeflag = tar.TypeReg
	header.Size = info.Size()
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	f, err := os.Open(hostPath)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() != info.Size() {
		return fmt.Errorf("backup source changed before copy: %s", archivePath)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tw, hash), f); err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("backup source changed during copy: %s", archivePath)
	}
	*checksums = append(*checksums, ChecksumEntry{Path: archivePath, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: info.Size(), Mode: int64(mode.Perm()), UID: header.Uid, GID: header.Gid})
	return nil
}

func Extract(archivePath, destination string, options ExtractOptions) (ExtractResult, error) {
	if !filepath.IsAbs(archivePath) || !filepath.IsAbs(destination) {
		return ExtractResult{}, fmt.Errorf("archive and destination must be absolute paths")
	}
	limits := options.Limits
	if limits.MaxEntries <= 0 || limits.MaxFileBytes <= 0 || limits.MaxTotalBytes <= 0 {
		limits = DefaultLimits()
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return ExtractResult{}, fmt.Errorf("create extraction destination: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(destination)
		}
	}()

	f, err := os.Open(archivePath)
	if err != nil {
		return ExtractResult{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("open compressed archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	observed := map[string]ChecksumEntry{}
	type extractedDirectory struct {
		name    string
		mode    fs.FileMode
		uid     int
		gid     int
		modTime time.Time
	}
	var directories []extractedDirectory
	var manifestBytes, checksumBytes []byte
	var entries int
	var total int64
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ExtractResult{}, fmt.Errorf("read archive: %w", err)
		}
		entries++
		if entries > limits.MaxEntries {
			return ExtractResult{}, fmt.Errorf("archive entry limit exceeded")
		}
		name, err := validateArchiveName(header.Name)
		if err != nil || seen[name] {
			return ExtractResult{}, fmt.Errorf("unsafe or duplicate archive entry %q", header.Name)
		}
		seen[name] = true
		if header.Size < 0 || header.Size > limits.MaxFileBytes || total > limits.MaxTotalBytes-header.Size {
			return ExtractResult{}, fmt.Errorf("archive size limit exceeded")
		}
		total += header.Size
		target := filepath.Join(destination, filepath.FromSlash(name))
		if !within(destination, target) {
			return ExtractResult{}, fmt.Errorf("archive entry escapes destination")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return ExtractResult{}, err
			}
			directories = append(directories, extractedDirectory{name: target, mode: fs.FileMode(header.Mode) & 0777, uid: header.Uid, gid: header.Gid, modTime: header.ModTime})
		case tar.TypeReg, tar.TypeRegA:
			if (name == "manifest.json" && header.Size > 4<<20) || (name == "checksums/sha256.json" && header.Size > 128<<20) {
				return ExtractResult{}, fmt.Errorf("archive metadata size limit exceeded")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return ExtractResult{}, err
			}
			contents, digest, err := extractRegular(tr, target, header, options.PreserveOwnership)
			if err != nil {
				return ExtractResult{}, err
			}
			observed[name] = ChecksumEntry{Path: name, SHA256: digest, Size: header.Size, Mode: header.Mode & 0777, UID: header.Uid, GID: header.Gid}
			if name == "manifest.json" {
				manifestBytes = contents
			}
			if name == "checksums/sha256.json" {
				checksumBytes = contents
				delete(observed, name)
			}
		default:
			return ExtractResult{}, fmt.Errorf("archive links and special files are not supported")
		}
	}
	if len(manifestBytes) == 0 || len(checksumBytes) == 0 {
		return ExtractResult{}, fmt.Errorf("archive metadata is incomplete")
	}
	var manifest Manifest
	if err := strictJSON(manifestBytes, &manifest); err != nil {
		return ExtractResult{}, fmt.Errorf("invalid backup manifest: %w", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return ExtractResult{}, err
	}
	var index ChecksumIndex
	if err := strictJSON(checksumBytes, &index); err != nil {
		return ExtractResult{}, fmt.Errorf("invalid checksum index: %w", err)
	}
	if err := verifyChecksums(index, observed); err != nil {
		return ExtractResult{}, err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		directory := directories[i]
		if err := os.Chmod(directory.name, directory.mode); err != nil {
			return ExtractResult{}, err
		}
		if err := os.Chtimes(directory.name, directory.modTime, directory.modTime); err != nil {
			return ExtractResult{}, err
		}
		if options.PreserveOwnership {
			if err := os.Chown(directory.name, directory.uid, directory.gid); err != nil {
				return ExtractResult{}, err
			}
		}
	}
	ok = true
	return ExtractResult{Manifest: manifest, Checksums: index}, nil
}

func extractRegular(reader io.Reader, target string, header *tar.Header, preserveOwnership bool) ([]byte, string, error) {
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("archive entry already exists")
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fs.FileMode(header.Mode)&0777)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	hash := sha256.New()
	var capture strings.Builder
	var writer io.Writer = io.MultiWriter(f, hash)
	if header.Name == archiveRoot+"/manifest.json" || header.Name == archiveRoot+"/checksums/sha256.json" {
		writer = io.MultiWriter(f, hash, &capture)
	}
	if _, err := io.CopyN(writer, reader, header.Size); err != nil {
		return nil, "", err
	}
	if err := f.Chmod(fs.FileMode(header.Mode) & 0777); err != nil {
		return nil, "", err
	}
	if err := os.Chtimes(target, header.ModTime, header.ModTime); err != nil {
		return nil, "", err
	}
	if preserveOwnership {
		if err := f.Chown(header.Uid, header.Gid); err != nil {
			return nil, "", err
		}
	}
	return []byte(capture.String()), hex.EncodeToString(hash.Sum(nil)), nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.Format != FormatName || manifest.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported backup format")
	}
	if manifest.KITProVersion == "" || manifest.CreatedAt.IsZero() || manifest.ApplicationID == "" || manifest.InstallationID == "" || manifest.ReleaseID == "" || manifest.RuntimeGeneration < 1 {
		return fmt.Errorf("backup manifest identity is incomplete")
	}
	if manifest.Strategy != "metadata-only" && manifest.Strategy != "cold-filesystem" && manifest.Strategy != "cold-sqlite-filesystem" {
		return fmt.Errorf("unsupported backup strategy")
	}
	if len(manifest.Components) == 0 {
		return fmt.Errorf("backup manifest has no components")
	}
	componentIDs := map[string]bool{}
	for _, component := range manifest.Components {
		if component.ID == "" || component.ImageDigest == "" || componentIDs[component.ID] {
			return fmt.Errorf("invalid backup component")
		}
		componentIDs[component.ID] = true
	}
	storage := map[string]bool{}
	for _, item := range manifest.Storage {
		if !componentIDs[item.Component] || item.ID == "" || (item.Disposition != "include" && item.Disposition != "exclude-ephemeral") {
			return fmt.Errorf("invalid backup storage")
		}
		key := item.Component + "/" + item.ID
		if storage[key] {
			return fmt.Errorf("duplicate backup storage")
		}
		storage[key] = true
		if item.Disposition == "include" {
			if canonical, err := canonicalPayloadPath(item.ArchivePath); err != nil || canonical != item.ArchivePath || !strings.HasPrefix(canonical, "application/") {
				return fmt.Errorf("invalid storage archive path")
			}
		} else if item.ArchivePath != "" {
			return fmt.Errorf("excluded storage cannot have archive content")
		}
	}
	if manifest.Checksum.Algorithm != "" && (manifest.Checksum.Algorithm != "sha256" || manifest.Checksum.IndexPath != "checksums/sha256.json") {
		return fmt.Errorf("unsupported checksum metadata")
	}
	return nil
}

func verifyChecksums(index ChecksumIndex, observed map[string]ChecksumEntry) error {
	if index.Algorithm != "sha256" || len(index.Files) != len(observed) {
		return fmt.Errorf("checksum index does not match archive payload")
	}
	seen := map[string]bool{}
	for _, item := range index.Files {
		name, err := canonicalPayloadPath(item.Path)
		if err != nil || name != item.Path || seen[name] || len(item.SHA256) != 64 {
			return fmt.Errorf("invalid checksum entry")
		}
		got, present := observed[name]
		if _, err := hex.DecodeString(item.SHA256); err != nil || !present || got.SHA256 != item.SHA256 || got.Size != item.Size || got.Mode != item.Mode || got.UID != item.UID || got.GID != item.GID {
			return fmt.Errorf("checksum mismatch for %s", name)
		}
		seen[name] = true
	}
	return nil
}

func canonicalPayloadPath(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.ContainsRune(name, '\x00') || path.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("unsafe archive path")
	}
	return name, nil
}

func validateArchiveName(name string) (string, error) {
	name = strings.TrimSuffix(name, "/")
	if !strings.HasPrefix(name, archiveRoot+"/") {
		return "", fmt.Errorf("entry outside archive root")
	}
	return canonicalPayloadPath(strings.TrimPrefix(name, archiveRoot+"/"))
}

func archiveName(name string) string { return archiveRoot + "/" + name }

func writeBytes(tw *tar.Writer, name string, contents []byte, mode fs.FileMode) error {
	header := &tar.Header{Name: archiveName(name), Typeflag: tar.TypeReg, Mode: int64(mode.Perm()), Size: int64(len(contents)), ModTime: time.Time{}, Format: tar.FormatPAX}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(contents)
	return err
}

func checksumBytes(name string, contents []byte, mode fs.FileMode) ChecksumEntry {
	sum := sha256.Sum256(contents)
	return ChecksumEntry{Path: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(contents)), Mode: int64(mode.Perm())}
}

func canonicalJSON(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func strictJSON(data []byte, destination any) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := walkJSON(decoder); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func walkJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate or invalid JSON field")
			}
			seen[key] = true
			if err := walkJSON(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}

func within(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func syncDirectory(directory string) error {
	f, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync archive directory: %w", err)
	}
	return nil
}
