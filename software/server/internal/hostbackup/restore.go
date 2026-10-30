package hostbackup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	controlbackup "github.com/kitpro/kitpro/software/server/internal/backup"
	_ "modernc.org/sqlite"
)

var restoreIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}$`)

type RestoreOptions struct {
	Archive   string
	StateRoot string
	Fault     func(point string) error
}

type restoreJournal struct {
	ID    string `json:"id"`
	Phase string `json:"phase"`
}

var restoreTargets = []string{"helper.db", "ingress/certificates", "acme"}
var databaseSidecarSuffixes = []string{"-wal", "-shm"}

func Restore(ctx context.Context, options RestoreOptions) error {
	root := cleanRoot(options.StateRoot)
	if root == "" || !filepath.IsAbs(options.Archive) {
		return fmt.Errorf("invalid host-state restore request")
	}
	if err := Recover(root); err != nil {
		return err
	}
	id := time.Now().UTC().Format("20060102T150405Z") + "-" + randomSuffix(options.Archive)
	staging := filepath.Join(root, ".host-restore-"+id)
	rollback := filepath.Join(root, ".host-restore-rollback-"+id)
	if err := os.MkdirAll(staging, 0700); err != nil {
		return err
	}
	defer func() {
		if _, err := os.Stat(journalPath(root)); os.IsNotExist(err) {
			_ = os.RemoveAll(staging)
			_ = os.RemoveAll(rollback)
		}
	}()
	manifest, err := extract(options.Archive, staging)
	if err != nil {
		return err
	}
	if err = controlbackup.Verify(ctx, filepath.Join(staging, "helper.db")); err != nil {
		return fmt.Errorf("restored helper database is invalid: %w", err)
	}
	if err = markIngressForReactivation(ctx, filepath.Join(staging, "helper.db")); err != nil {
		return err
	}
	for _, path := range restoreTargets[1:] {
		if err = os.MkdirAll(filepath.Join(staging, path), 0700); err != nil {
			return err
		}
	}
	if manifest.RemoteCloudflareStateIncluded || manifest.GeneratedIngressRuntimeIncluded {
		return fmt.Errorf("host-state backup makes unsupported remote/runtime claims")
	}
	if err = os.MkdirAll(rollback, 0700); err != nil {
		return err
	}
	for _, suffix := range databaseSidecarSuffixes {
		if _, statErr := os.Lstat(filepath.Join(staging, "helper.db") + suffix); statErr == nil {
			return fmt.Errorf("restored helper database contains an unexpected %s sidecar", suffix)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
	}
	if _, mainErr := os.Lstat(filepath.Join(root, "helper.db")); os.IsNotExist(mainErr) {
		for _, suffix := range databaseSidecarSuffixes {
			if _, sidecarErr := os.Lstat(filepath.Join(root, "helper.db") + suffix); sidecarErr == nil {
				return fmt.Errorf("helper database sidecar exists without its database")
			} else if !os.IsNotExist(sidecarErr) {
				return sidecarErr
			}
		}
	} else if mainErr != nil {
		return mainErr
	}
	if err = writeJournal(root, restoreJournal{ID: id, Phase: "swapping"}); err != nil {
		return err
	}
	for _, relative := range restoreTargets {
		target := filepath.Join(root, relative)
		previous := filepath.Join(rollback, relative)
		candidate := filepath.Join(staging, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if _, statErr := os.Lstat(target); statErr == nil {
			if err = os.MkdirAll(filepath.Dir(previous), 0700); err != nil {
				return err
			}
			if err = os.Rename(target, previous); err != nil {
				return err
			}
			if relative == "helper.db" {
				for _, suffix := range databaseSidecarSuffixes {
					if err = moveIfPresent(target+suffix, previous+suffix); err != nil {
						return err
					}
				}
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if err = os.Rename(candidate, target); err != nil {
			return err
		}
		if options.Fault != nil {
			if faultErr := options.Fault("after-" + strings.ReplaceAll(relative, "/", "-")); faultErr != nil {
				if faultErr == ErrInterrupted {
					return faultErr
				}
				_ = Recover(root)
				return faultErr
			}
		}
	}
	if err = deactivateIngressRuntime(root); err != nil {
		_ = Recover(root)
		return err
	}
	if err = writeJournal(root, restoreJournal{ID: id, Phase: "committed"}); err != nil {
		_ = Recover(root)
		return err
	}
	return Recover(root)
}

func Recover(root string) error {
	root = cleanRoot(root)
	if root == "" {
		return fmt.Errorf("invalid helper state root")
	}
	encoded, err := os.ReadFile(journalPath(root))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal restoreJournal
	if json.Unmarshal(encoded, &journal) != nil || !restoreIDPattern.MatchString(journal.ID) || (journal.Phase != "swapping" && journal.Phase != "committed") {
		return fmt.Errorf("invalid host-state restore journal")
	}
	staging := filepath.Join(root, ".host-restore-"+journal.ID)
	rollback := filepath.Join(root, ".host-restore-rollback-"+journal.ID)
	if journal.Phase == "swapping" {
		for index := len(restoreTargets) - 1; index >= 0; index-- {
			relative := restoreTargets[index]
			target, previous, candidate := filepath.Join(root, relative), filepath.Join(rollback, relative), filepath.Join(staging, relative)
			if _, previousErr := os.Lstat(previous); previousErr == nil {
				_ = os.RemoveAll(target)
				if relative == "helper.db" {
					for _, suffix := range databaseSidecarSuffixes {
						_ = os.RemoveAll(target + suffix)
					}
				}
				if err = os.MkdirAll(filepath.Dir(target), 0700); err == nil {
					err = os.Rename(previous, target)
				}
				if err != nil {
					return err
				}
				if relative == "helper.db" {
					for _, suffix := range databaseSidecarSuffixes {
						if err = moveIfPresent(previous+suffix, target+suffix); err != nil {
							return err
						}
					}
				}
			} else if os.IsNotExist(previousErr) {
				if _, candidateErr := os.Lstat(candidate); os.IsNotExist(candidateErr) {
					if err = os.RemoveAll(target); err != nil {
						return err
					}
					if relative == "helper.db" {
						for _, suffix := range databaseSidecarSuffixes {
							if err = os.RemoveAll(target + suffix); err != nil {
								return err
							}
						}
					}
				}
			} else {
				return previousErr
			}
		}
	}
	if err = os.RemoveAll(staging); err != nil {
		return err
	}
	if err = os.RemoveAll(rollback); err != nil {
		return err
	}
	if err = os.Remove(journalPath(root)); err != nil {
		return err
	}
	return syncDirectory(root)
}

func moveIfPresent(source, destination string) error {
	if _, err := os.Lstat(source); err == nil {
		return os.Rename(source, destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func extract(archive, destination string) (Manifest, error) {
	file, err := os.Open(archive)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	observed := map[string]Entry{}
	var manifest Manifest
	var total int64
	for count := 0; ; count++ {
		if count > maxEntries+1 {
			return Manifest{}, fmt.Errorf("host-state archive has too many entries")
		}
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxFileBytes {
			return Manifest{}, fmt.Errorf("invalid host-state archive entry")
		}
		prefix := archiveRoot + "/"
		if !strings.HasPrefix(header.Name, prefix) {
			return Manifest{}, fmt.Errorf("invalid host-state archive root")
		}
		relative := filepath.FromSlash(strings.TrimPrefix(header.Name, prefix))
		if !safeRelative(relative) || filepath.ToSlash(relative) != strings.TrimPrefix(header.Name, prefix) {
			return Manifest{}, fmt.Errorf("invalid host-state archive path")
		}
		total += header.Size
		if total > maxTotalBytes {
			return Manifest{}, fmt.Errorf("host-state archive is too large")
		}
		if relative == "manifest.json" {
			if manifest.Format != "" || json.NewDecoder(io.LimitReader(tarReader, header.Size)).Decode(&manifest) != nil {
				return Manifest{}, fmt.Errorf("invalid host-state manifest")
			}
			continue
		}
		if _, exists := observed[filepath.ToSlash(relative)]; exists {
			return Manifest{}, fmt.Errorf("duplicate host-state archive path")
		}
		target := filepath.Join(destination, relative)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return Manifest{}, err
		}
		output, createErr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if createErr != nil {
			return Manifest{}, createErr
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(tarReader, header.Size))
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return Manifest{}, fmt.Errorf("extract host-state entry failed")
		}
		observed[filepath.ToSlash(relative)] = Entry{Path: filepath.ToSlash(relative), SHA256: hex.EncodeToString(hash.Sum(nil)), Size: written, Mode: 0600}
	}
	if err = validateManifest(manifest, observed); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest Manifest, observed map[string]Entry) error {
	if manifest.Format != Format || manifest.FormatVersion != FormatVersion || !manifest.CertificatePrivateKeysIncluded || len(manifest.Entries) != len(observed) {
		return fmt.Errorf("unsupported host-state manifest")
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	seen := map[string]bool{}
	for _, expected := range manifest.Entries {
		actual, ok := observed[expected.Path]
		if seen[expected.Path] || !ok || actual.SHA256 != expected.SHA256 || actual.Size != expected.Size || expected.Mode != 0600 {
			return fmt.Errorf("host-state archive integrity check failed")
		}
		seen[expected.Path] = true
	}
	return nil
}

func markIngressForReactivation(ctx context.Context, databasePath string) error {
	db, err := sql.Open("sqlite", "file:"+databasePath+"?mode=rw&_pragma=foreign_keys(1)&_pragma=busy_timeout(250)")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE ingress_generations SET status='rolled_back',failure_code='restore_requires_reactivation' WHERE status='active'`); err != nil {
		_ = db.Close()
		return err
	}
	return sealStandaloneDatabase(ctx, db, databasePath)
}

func deactivateIngressRuntime(root string) error {
	current := filepath.Join(root, "ingress", "current")
	bootstrap := filepath.Join(root, "ingress", "bootstrap")
	if _, err := os.Stat(bootstrap); err != nil {
		return fmt.Errorf("ingress bootstrap generation unavailable")
	}
	if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(bootstrap, current)
}

func writeJournal(root string, journal restoreJournal) error {
	encoded, _ := json.Marshal(journal)
	temporary := journalPath(root) + ".new"
	if err := os.WriteFile(temporary, encoded, 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, journalPath(root)); err != nil {
		return err
	}
	return syncDirectory(root)
}

func journalPath(root string) string { return filepath.Join(root, "host-restore.json") }

func randomSuffix(value string) string {
	digest := sha256.Sum256([]byte(value + time.Now().UTC().String()))
	return hex.EncodeToString(digest[:4])
}
