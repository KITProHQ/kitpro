package hardware

import "testing"

func TestUnknownClassFailsClosed(t *testing.T) {
	if _, err := (Inventory{}).Resolve("/dev/sda"); err == nil {
		t.Fatal("raw device accepted")
	}
	for _, class := range []string{"/dev/mem", "/dev/kvm", "/dev/input/event0", "usb.any", "tty"} {
		if _, err := (Inventory{}).Resolve(class); err == nil {
			t.Fatalf("unsafe class %q accepted", class)
		}
	}
}

func TestResolveNVIDIARequiresDriverAndRuntime(t *testing.T) {
	base := Inventory{Accelerators: []Accelerator{{Vendor: "nvidia", StableID: "0000:01:00.0:10de:2489"}}}
	if _, err := base.Resolve(NVIDIAClass); err == nil {
		t.Fatal("NVIDIA accepted without integration")
	}
	base.NVIDIADriver, base.NVIDIARuntime = true, true
	a, err := base.Resolve(NVIDIAClass)
	if err != nil || !a.NVIDIARuntime || len(a.Devices) != 0 {
		t.Fatalf("assignment=%#v err=%v", a, err)
	}
	base.Accelerators = append(base.Accelerators, Accelerator{Vendor: "nvidia", StableID: "0000:02:00.0:10de:2489"})
	if _, err := base.Resolve(NVIDIAClass); err == nil {
		t.Fatal("ambiguous multi-GPU host accepted")
	}
}

func TestResolveVAAPIMapsOneVendorRenderNode(t *testing.T) {
	n := Node{Kind: "render", Path: "/dev/dri/renderD129", Major: 226, Minor: 129}
	inv := Inventory{Accelerators: []Accelerator{{Vendor: "amd", StableID: "0000:06:00.0:1002:164e", RenderNodes: []Node{n}}}}
	a, err := inv.Resolve(VAAPIClass)
	if err != nil || len(a.Devices) != 1 || a.Devices[0].Path != n.Path {
		t.Fatalf("assignment=%#v err=%v", a, err)
	}
}
