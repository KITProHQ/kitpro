package appbackup

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	controlbackup "github.com/kitpro/kitpro/software/server/internal/backup"
	"golang.org/x/sys/unix"
)

type ManagedSource struct {
	Component string
	StorageID string
	Path      string
	OwnerUID  int
	OwnerGID  int
}

func TreeSize(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("managed storage contains unsupported file type")
		}
		if info.Mode().IsRegular() {
			if size > (1<<63-1)-info.Size() {
				return fmt.Errorf("managed storage size overflow")
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func RequireAvailableSpace(path string, required int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return fmt.Errorf("inspect destination capacity: %w", err)
	}
	available := int64(stat.Bavail) * int64(stat.Bsize)
	if required < 0 || available < required {
		return fmt.Errorf("insufficient backup storage space: need %d bytes, have %d", required, available)
	}
	return nil
}

func CopyTree(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed storage source is not a real directory")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("staging destination already exists")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	type directoryMetadata struct {
		name string
		info fs.FileInfo
	}
	directories := []directoryMetadata{{name: destination, info: info}}
	if err := filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == source {
			return nil
		}
		rel, err := filepath.Rel(source, name)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("managed storage path escapes source")
		}
		target := filepath.Join(destination, rel)
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		mode := entryInfo.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsDir() && !mode.IsRegular()) {
			return fmt.Errorf("managed storage contains unsupported file type")
		}
		if mode.IsDir() {
			if err := os.Mkdir(target, 0700); err != nil {
				return err
			}
			directories = append(directories, directoryMetadata{name: target, info: entryInfo})
			return nil
		}
		return copyRegular(name, target, entryInfo)
	}); err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		directory := directories[i]
		if err := os.Chmod(directory.name, directory.info.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Chtimes(directory.name, directory.info.ModTime(), directory.info.ModTime()); err != nil {
			return err
		}
		if err := applyOwnership(directory.name, directory.info); err != nil {
			return err
		}
	}
	return nil
}

func copyRegular(source, destination string, info fs.FileInfo) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return fmt.Errorf("managed storage file changed before copy")
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	copyErr := error(nil)
	if _, err = io.Copy(out, in); err != nil {
		copyErr = err
	} else if err = out.Sync(); err != nil {
		copyErr = err
	} else if err = out.Chmod(info.Mode().Perm()); err != nil {
		copyErr = err
	} else if err = os.Chtimes(destination, info.ModTime(), info.ModTime()); err != nil {
		copyErr = err
	} else if err = applyOwnershipFile(out, info); err != nil {
		copyErr = err
	}
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	after, err := in.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("managed storage file changed during copy")
	}
	return nil
}

func applyOwnership(name string, info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Chown(name, int(stat.Uid), int(stat.Gid))
}

func applyOwnershipFile(file *os.File, info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return file.Chown(int(stat.Uid), int(stat.Gid))
}

func DiscoverAndVerifySQLite(ctx context.Context, sources []ManagedSource) ([]Database, error) {
	var databases []Database
	for _, source := range sources {
		err := filepath.WalkDir(source.Path, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			isSQLite, err := hasSQLiteHeader(name)
			if err != nil {
				return err
			}
			if !isSQLite {
				return nil
			}
			if err := verifySQLiteSnapshot(ctx, source.Path, name); err != nil {
				return fmt.Errorf("SQLite verification failed for %s/%s: %w", source.Component, source.StorageID, err)
			}
			relative, err := filepath.Rel(source.Path, name)
			if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("SQLite path escapes managed storage")
			}
			databases = append(databases, Database{Engine: "sqlite", Component: source.Component, StorageID: source.StorageID, RelativePath: filepath.ToSlash(relative), IntegrityCheck: "passed"})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(databases, func(i, j int) bool {
		left := databases[i].Component + "/" + databases[i].StorageID + "/" + databases[i].RelativePath
		right := databases[j].Component + "/" + databases[j].StorageID + "/" + databases[j].RelativePath
		return left < right
	})
	return databases, nil
}

func verifySQLiteSnapshot(ctx context.Context, storageRoot, databasePath string) error {
	workspace, err := os.MkdirTemp(filepath.Dir(storageRoot), ".sqlite-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)

	verifiedPath := filepath.Join(workspace, filepath.Base(databasePath))
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		source := databasePath + suffix
		info, statErr := os.Lstat(source)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("SQLite snapshot companion is not a regular file")
		}
		destination := verifiedPath + suffix
		if err = copyRegular(source, destination, info); err != nil {
			return err
		}
		if err = os.Chown(destination, os.Geteuid(), os.Getegid()); err != nil {
			return err
		}
		if err = os.Chmod(destination, 0600); err != nil {
			return err
		}
	}
	return controlbackup.VerifyWritableSnapshot(ctx, verifiedPath)
}

func hasSQLiteHeader(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var header [16]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return false, nil
		}
		return false, err
	}
	return string(header[:]) == "SQLite format 3\x00", nil
}
