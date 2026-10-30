package hostadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type storageFixtureRunner struct {
	values map[string][]byte
	errors map[string]error
}

func (r storageFixtureRunner) Run(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, ErrUnavailable
	}
	if err := r.errors[name]; err != nil {
		return nil, err
	}
	return r.values[name], nil
}

const blockFixture = `{"blockdevices":[
 {"name":"/dev/sda","kname":"sda","path":"/dev/sda","type":"disk","maj:min":"8:0","size":1000000,"rota":true,"ro":false,"rm":false,"model":"Disk <unsafe>","vendor":"ATA","tran":"sata","fstype":null,"uuid":null,"label":null,"mountpoints":[null],"children":[
  {"name":"/dev/sda1","kname":"sda1","path":"/dev/sda1","type":"part","pkname":"sda","maj:min":"8:1","size":900000,"rota":true,"ro":false,"rm":false,"fstype":"ext4","uuid":"root-id","label":"root","mountpoints":["/"]}
 ]},
 {"name":"/dev/vda","kname":"vda","path":"/dev/vda","type":"disk","maj:min":"252:0","size":2000000,"rota":false,"ro":false,"rm":false,"model":"QEMU disk","vendor":"","tran":"virtio","mountpoints":[null]},
 {"name":"/dev/dm-0","kname":"dm-0","path":"/dev/mapper/vg-data","type":"lvm","maj:min":"253:0","size":500000,"rota":false,"ro":false,"rm":false,"fstype":"xfs","uuid":"data-id","label":"data","mountpoints":["/data"]},
 {"name":"/dev/loop0","kname":"loop0","path":"/dev/loop0","type":"loop","maj:min":"7:0","size":1000,"rota":false,"ro":true,"rm":false,"fstype":"squashfs","mountpoints":["/snap/test"]}
]}`

const mountFixture = `36 25 8:1 / / rw,relatime - ext4 /dev/sda1 rw
37 36 8:1 /srv /srv/data rw,bind - ext4 /dev/sda1 rw
38 36 253:0 / /data rw,relatime - xfs /dev/mapper/vg-data rw
39 36 0:45 / /run rw,nosuid - tmpfs tmpfs rw
40 36 0:46 / /var/lib/docker/overlay2/test/merged rw,relatime - overlay overlay rw
41 36 0:47 / /mnt/share ro,relatime - nfs4 server:/share ro
42 36 7:0 / /snap/test ro - squashfs /dev/loop0 ro
`

func storageFixture() StorageProvider {
	return StorageProvider{
		Runner: storageFixtureRunner{values: map[string][]byte{
			"lsblk": []byte(blockFixture),
			"lvm":   []byte(`{"report":[{"lv":[{"lv_name":"data","vg_name":"vg","lv_path":"/dev/vg/data","lv_size":"500000","lv_attr":"-wi-a-----","pool_lv":"","origin":""}]}]}`),
			"zpool": []byte("tank\t1000\t400\t600\tONLINE\n"),
			"zfs":   []byte("tank/data\tfilesystem\t400\t600\t/tank/data\n"),
		}},
		ReadMountInfo: func() ([]byte, error) { return []byte(mountFixture), nil },
		ReadSwaps: func() ([]byte, error) {
			return []byte("Filename Type Size Used Priority\n/dev/sda2 partition 1 0 -2\n"), nil
		},
		StatFS: func(path string) (StorageStat, error) {
			if path == "/snap/test" {
				return StorageStat{}, errors.New("device disappeared")
			}
			if path == "/run" {
				return StorageStat{}, errors.New("confinement denied memory mount")
			}
			if path == "/data" {
				return StorageStat{TotalBytes: 1000, FreeBytes: 20, AvailableBytes: 10}, nil
			}
			return StorageStat{TotalBytes: 1000, FreeBytes: 200, AvailableBytes: 150}, nil
		},
	}
}

func TestStorageInventoryRelationshipsCapacityAndCapabilities(t *testing.T) {
	inventory, err := storageFixture().Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Devices) != 5 || inventory.Summary.DeviceCount != 5 {
		t.Fatalf("devices: %#v", inventory.Summary)
	}
	var physical, virtual, partition, logical bool
	for _, item := range inventory.Devices {
		switch item.Category {
		case "physical_disk":
			physical = true
		case "virtual_disk":
			virtual = true
		case "partition":
			partition = item.ParentID != ""
		case "logical":
			logical = true
		}
	}
	if !physical || !virtual || !partition || !logical {
		t.Fatalf("categories missing: %#v", inventory.Devices)
	}
	if inventory.Summary.TotalBytes != 2000 || inventory.Summary.UsedBytes != 1780 || inventory.Summary.AvailableBytes != 160 {
		t.Fatalf("summary double counting or capacity error: %#v", inventory.Summary)
	}
	if inventory.Capabilities["lvm"].State != Available || len(inventory.LVMVolumes) != 1 || inventory.Capabilities["zfs"].State != Available || len(inventory.ZFSPools) != 1 {
		t.Fatalf("advanced inventory missing: %#v", inventory.Capabilities)
	}
}

func TestStorageMountClassesProtectionAndWarnings(t *testing.T) {
	inventory, err := storageFixture().Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byPoint := map[string]Filesystem{}
	for _, item := range inventory.Filesystems {
		byPoint[item.MountPoint] = item
	}
	if !contains(byPoint["/"].Protection, "root_filesystem") || !contains(byPoint["/"].Protection, "kitpro_state") || !contains(byPoint["/"].Protection, "container_storage") {
		t.Fatalf("root protections: %#v", byPoint["/"].Protection)
	}
	if !byPoint["/srv/data"].Bind || !byPoint["/mnt/share"].Network || !byPoint["/run"].MemoryBacked || !byPoint["/var/lib/docker/overlay2/test/merged"].Virtual {
		t.Fatalf("mount classification: %#v", byPoint)
	}
	if !contains(byPoint["/data"].Warnings, "capacity_critical") || !contains(byPoint["/mnt/share"].Warnings, "mounted_read_only") {
		t.Fatalf("warnings: %#v %#v", byPoint["/data"], byPoint["/mnt/share"])
	}
	if contains(byPoint["/snap/test"].Warnings, "mounted_read_only") {
		t.Fatal("expected read-only pseudo filesystem reported as a warning")
	}
	if contains(byPoint["/snap/test"].Warnings, "capacity_unavailable") || contains(byPoint["/snap/test"].Warnings, "capacity_critical") {
		t.Fatal("virtual filesystem capacity inflated persistent storage warnings")
	}
	if byPoint["/snap/test"].Status.State != Unavailable || byPoint["/snap/test"].TotalBytes != nil {
		t.Fatalf("disappearing device treated as available: %#v", byPoint["/snap/test"])
	}
	if byPoint["/run"].Status.State != Unavailable || contains(byPoint["/run"].Warnings, "capacity_unavailable") {
		t.Fatalf("unavailable memory filesystem inflated persistent storage warnings: %#v", byPoint["/run"])
	}
}

func TestMountInfoEscapingAndMalformedInput(t *testing.T) {
	records, err := parseMountInfo([]byte("1 0 8:1 /path\\040with\\040space /mnt\\040space rw - ext4 /dev/test rw\nmalformed\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Point != "/mnt space" || records[0].Root != "/path with space" {
		t.Fatalf("escape parsing: %#v", records)
	}
	tooLarge := []byte(strings.Repeat("x", maxStorageOutput+1))
	if _, err = parseMountInfo(tooLarge); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("oversized mountinfo accepted: %v", err)
	}
}

func TestBtrfsSubvolumeAndUnknownFilesystemRemainGenericMounts(t *testing.T) {
	if isBindMount(mountRecord{Root: "/@", FSType: "btrfs"}) {
		t.Fatal("Btrfs subvolume mislabeled as bind mount")
	}
	if isBindMount(mountRecord{Root: "/dataset", FSType: "zfs"}) {
		t.Fatal("ZFS dataset mislabeled as bind mount")
	}
	if !isBindMount(mountRecord{Root: "/source", FSType: "ext4"}) {
		t.Fatal("bind mount not identified")
	}
	for _, kind := range []string{"btrfs", "f2fs", "bcachefs"} {
		network, memory, virtual := fsKinds(kind)
		if network || memory || virtual {
			t.Fatalf("generic filesystem %s misclassified", kind)
		}
	}
}

func TestOptionalStorageCapabilityStates(t *testing.T) {
	for _, test := range []struct {
		err  error
		want State
	}{{ErrUnsupported, Unsupported}, {ErrPermission, PermissionDenied}, {ErrUnavailable, Unavailable}} {
		provider := storageFixture()
		provider.Runner = storageFixtureRunner{values: map[string][]byte{"lsblk": []byte(blockFixture)}, errors: map[string]error{"lvm": test.err, "zpool": test.err}}
		inventory, err := provider.Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if inventory.Capabilities["lvm"].State != test.want || inventory.Capabilities["zfs"].State != test.want {
			t.Fatalf("%v: %#v", test.err, inventory.Capabilities)
		}
	}
}

func TestStorageMalformedAndContextTimeout(t *testing.T) {
	provider := storageFixture()
	provider.Runner = storageFixtureRunner{values: map[string][]byte{"lsblk": []byte("not json")}}
	if _, err := provider.Collect(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("malformed lsblk: %v", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := storageFixture().Collect(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired context: %v", err)
	}
	provider = storageFixture()
	provider.Runner = storageFixtureRunner{values: map[string][]byte{"lsblk": []byte(strings.Repeat("x", maxStorageOutput+1))}}
	if _, err := provider.Collect(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("oversized command output: %v", err)
	}
}

func TestStorageInventoryBounds(t *testing.T) {
	lines := make([]string, MaxStorageFilesystems+1)
	for i := range lines {
		lines[i] = fmt.Sprintf("%d 0 8:1 / /m%d rw - ext4 /dev/test rw", i+1, i)
	}
	if _, err := parseMountInfo([]byte(strings.Join(lines, "\n"))); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mount limit not enforced: %v", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
