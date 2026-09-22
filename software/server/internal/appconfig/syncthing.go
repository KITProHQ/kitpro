// Package appconfig applies narrowly reviewed structured policies to managed
// application configuration. It deliberately has no template or command API.
package appconfig

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const SyncthingTCPOnlyV1 = "syncthing-tcp-only-v1"

var syncthingTCPOnlyValues = map[string]string{
	"listenAddress":         "tcp://0.0.0.0:22000",
	"globalAnnounceEnabled": "false",
	"localAnnounceEnabled":  "false",
	"relaysEnabled":         "false",
	"natEnabled":            "false",
	"startBrowser":          "false",
	"urAccepted":            "-1",
	"autoUpgradeIntervalH":  "0",
	"crashReportingEnabled": "false",
}

// Apply enforces a known policy on an existing application-generated config.
func Apply(policy, managedRoot string) error {
	if policy != SyncthingTCPOnlyV1 {
		return errors.New("unsupported structured configuration policy")
	}
	if err := validateManagedConfigurationDirectories(managedRoot); err != nil {
		return err
	}
	return applySyncthingTCPOnly(filepath.Join(managedRoot, "config", "config.xml"))
}

// Verify confirms that the persisted configuration still expresses the
// complete bounded policy. It does not inspect application-managed peers or
// folders.
func Verify(policy, managedRoot string) error {
	if policy != SyncthingTCPOnlyV1 {
		return errors.New("unsupported structured configuration policy")
	}
	if err := validateManagedConfigurationDirectories(managedRoot); err != nil {
		return err
	}
	data, err := readRegularBounded(filepath.Join(managedRoot, "config", "config.xml"))
	if err != nil {
		return err
	}
	values, counts, err := syncthingOptionValues(data)
	if err != nil {
		return err
	}
	for name, want := range syncthingTCPOnlyValues {
		if counts[name] != 1 || values[name] != want {
			return fmt.Errorf("Syncthing configuration policy mismatch for %s", name)
		}
	}
	return nil
}

func validateManagedConfigurationDirectories(managedRoot string) error {
	for _, path := range []string{managedRoot, filepath.Join(managedRoot, "config")} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Syncthing configuration path is not a managed directory")
		}
	}
	return nil
}

func applySyncthingTCPOnly(path string) error {
	data, err := readRegularBounded(path)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	decoder := xml.NewDecoder(bytes.NewReader(data))
	encoder := xml.NewEncoder(&output)
	inOptions := false
	optionsCount := 0
	seen := map[string]int{}
	for {
		token, tokenErr := decoder.Token()
		if tokenErr == io.EOF {
			break
		}
		if tokenErr != nil {
			return fmt.Errorf("parse Syncthing configuration: %w", tokenErr)
		}
		switch item := token.(type) {
		case xml.StartElement:
			if item.Name.Local == "options" {
				if inOptions {
					return errors.New("nested Syncthing options element")
				}
				inOptions = true
				optionsCount++
			}
			if inOptions {
				if replacement, ok := syncthingTCPOnlyValues[item.Name.Local]; ok {
					seen[item.Name.Local]++
					if item.Name.Local == "listenAddress" && seen[item.Name.Local] > 1 {
						if err = discardElement(decoder, item); err != nil {
							return err
						}
						continue
					}
					if err = encoder.EncodeToken(item); err != nil {
						return err
					}
					if err = encoder.EncodeToken(xml.CharData(replacement)); err != nil {
						return err
					}
					if err = discardElement(decoder, item); err != nil {
						return err
					}
					if err = encoder.EncodeToken(item.End()); err != nil {
						return err
					}
					continue
				}
			}
		case xml.EndElement:
			if item.Name.Local == "options" {
				inOptions = false
			}
		}
		if err = encoder.EncodeToken(token); err != nil {
			return err
		}
	}
	if err = encoder.Flush(); err != nil {
		return err
	}
	if optionsCount != 1 {
		return errors.New("Syncthing configuration must contain exactly one options element")
	}
	for name := range syncthingTCPOnlyValues {
		if seen[name] == 0 {
			return fmt.Errorf("Syncthing configuration is missing required option %s", name)
		}
	}
	if err = writeAtomicLike(path, output.Bytes()); err != nil {
		return err
	}
	return Verify(SyncthingTCPOnlyV1, filepath.Dir(filepath.Dir(path)))
}

func discardElement(decoder *xml.Decoder, start xml.StartElement) error {
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("parse Syncthing option %s: %w", start.Name.Local, err)
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

func syncthingOptionValues(data []byte) (map[string]string, map[string]int, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	values := map[string]string{}
	counts := map[string]int{}
	inOptions := false
	optionsCount := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		switch item := token.(type) {
		case xml.StartElement:
			if item.Name.Local == "options" {
				inOptions = true
				optionsCount++
				continue
			}
			if inOptions {
				if _, ok := syncthingTCPOnlyValues[item.Name.Local]; ok {
					var value string
					if err = decoder.DecodeElement(&value, &item); err != nil {
						return nil, nil, err
					}
					counts[item.Name.Local]++
					values[item.Name.Local] = strings.TrimSpace(value)
				}
			}
		case xml.EndElement:
			if item.Name.Local == "options" {
				inOptions = false
			}
		}
	}
	if optionsCount != 1 {
		return nil, nil, errors.New("Syncthing configuration must contain exactly one options element")
	}
	return values, counts, nil
}

func readRegularBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return nil, errors.New("Syncthing configuration is not a bounded regular file")
	}
	return os.ReadFile(path)
}

func writeAtomicLike(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kitpro-syncthing-config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	ok := false
	defer func() {
		_ = temporary.Close()
		if !ok {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if stat, valid := info.Sys().(*syscall.Stat_t); valid {
		if err = temporary.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
			return err
		}
	}
	if _, err = temporary.Write(data); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return err
	}
	ok = true
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
