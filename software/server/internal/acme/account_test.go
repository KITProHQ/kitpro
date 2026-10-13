package acmelifecycle

import (
	"crypto/ecdsa"
	"os"
	"path/filepath"
	"testing"
)

func TestAccountStorePersistsRestrictedStableKey(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	store := AccountStore{Root: root}
	first, err := store.LoadOrCreate(DirectoryStaging)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.LoadOrCreate(DirectoryStaging)
	if err != nil {
		t.Fatal(err)
	}
	if first.Public().(*ecdsa.PublicKey).X.Cmp(second.Public().(*ecdsa.PublicKey).X) != 0 {
		t.Fatal("account key changed")
	}
	info, err := os.Stat(filepath.Join(root, DirectoryStaging+".account-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("account key mode=%v", info.Mode().Perm())
	}
}

func TestAccountStoreRejectsPathAndDirectoryInjection(t *testing.T) {
	for _, store := range []AccountStore{{Root: "relative"}, {Root: t.TempDir() + "/../unsafe"}} {
		if _, err := store.LoadOrCreate(DirectoryProduction); err == nil {
			t.Fatal("accepted unsafe account root")
		}
	}
	if _, err := (AccountStore{Root: t.TempDir()}).LoadOrCreate("../../host"); err == nil {
		t.Fatal("accepted directory injection")
	}
}
