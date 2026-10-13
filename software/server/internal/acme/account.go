package acmelifecycle

import (
	"crypto"
	"fmt"
	"os"
	"path/filepath"
)

type AccountStore struct{ Root string }

func (s AccountStore) LoadOrCreate(directory string) (crypto.Signer, error) {
	root := s.Root
	if root == "" {
		root = "/var/lib/kitpro-helper/acme"
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, fmt.Errorf("invalid ACME account store")
	}
	if directory != DirectoryProduction && directory != DirectoryStaging {
		return nil, fmt.Errorf("invalid ACME directory")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("create ACME account store: %w", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(root, directory+".account-key.pem")
	value, err := os.ReadFile(keyPath)
	if err == nil {
		return ParseAccountKey(value)
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read ACME account key: %w", err)
	}
	key, encoded, err := NewAccountKey()
	if err != nil {
		return nil, fmt.Errorf("generate ACME account key: %w", err)
	}
	file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		value, err = os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read concurrently-created ACME account key: %w", err)
		}
		return ParseAccountKey(value)
	}
	if err != nil {
		return nil, fmt.Errorf("create ACME account key: %w", err)
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(keyPath)
		return nil, err
	}
	if closeErr != nil {
		_ = os.Remove(keyPath)
		return nil, closeErr
	}
	return key, nil
}
