package hostadmin

import (
	"context"
	"os"
	"testing"
	"time"
)

// Explicit opt-in: this test only reads host services and journal metadata.
func TestLiveReadOnlyHostProviders(t *testing.T) {
	if os.Getenv("KITPRO_TEST_LIVE_HOST_READONLY") != "1" {
		t.Skip("live host inspection requires explicit test opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	services, err := NewServiceProvider().List(ctx, ServiceQuery{State: "all", Limit: 1})
	if err != nil {
		t.Fatalf("systemd inventory: %v", err)
	}
	if services.Total == 0 {
		t.Fatal("systemd returned no services")
	}
	if _, err := NewServiceProvider().Detail(ctx, services.Items[0].Name); err != nil {
		t.Fatalf("systemd detail: %v", err)
	}
	now := time.Now().UTC()
	page, err := NewJournalProvider().Query(ctx, JournalQuery{Limit: 1, Since: now.Add(-time.Hour), Until: now})
	if err != nil {
		t.Fatalf("journal query: %v", err)
	}
	if page.NextCursor != "" {
		next, err := NewJournalProvider().Query(ctx, JournalQuery{Limit: 1, Since: now.Add(-time.Hour), Until: now, Cursor: page.NextCursor})
		if err != nil {
			t.Fatalf("journal next page: %v", err)
		}
		if len(next.Entries) > 0 && next.Entries[0].Cursor == page.NextCursor {
			t.Fatal("cursor page repeated entry")
		}
	}
}

func TestLiveReadOnlyStorageProvider(t *testing.T) {
	if os.Getenv("KITPRO_TEST_LIVE_HOST_READONLY") != "1" {
		t.Skip("live host inspection requires explicit test opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	inventory, err := NewStorageProvider().Collect(ctx)
	if err != nil {
		t.Fatalf("storage inventory: %v", err)
	}
	if len(inventory.Devices) == 0 || len(inventory.Filesystems) == 0 {
		t.Fatalf("incomplete storage inventory: %d devices, %d filesystems", len(inventory.Devices), len(inventory.Filesystems))
	}
	root := false
	for _, filesystem := range inventory.Filesystems {
		if filesystem.MountPoint == "/" {
			root = true
			break
		}
	}
	if !root {
		t.Fatal("root filesystem not identified")
	}
}
