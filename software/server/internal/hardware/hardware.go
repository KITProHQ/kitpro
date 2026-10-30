// Package hardware discovers the small, security-relevant accelerator surface
// that KITPro may expose through registered device classes.
package hardware

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	NVIDIAClass      = "gpu.nvidia"
	AMDClass         = "gpu.amd"
	IntelRenderClass = "gpu.intel.render"
	VAAPIClass       = "video.vaapi"
)

func ValidClass(class string) bool {
	switch class {
	case NVIDIAClass, AMDClass, IntelRenderClass, VAAPIClass:
		return true
	}
	return false
}

type Node struct {
	Kind, StableID, Path string
	Major, Minor         uint32
}
type Accelerator struct {
	Vendor, VendorID, DeviceID, Model, StableID string
	Cards, RenderNodes                          []Node
	KFD                                         bool
	KFDNode                                     Node
}
type Inventory struct {
	Accelerators  []Accelerator
	NVIDIADriver  bool
	NVIDIARuntime bool
	IOMMU         bool
}
type Assignment struct {
	Class, Vendor, StableID string
	Devices                 []Node
	NVIDIARuntime           bool
}

type Discoverer struct {
	Root          string
	NVIDIARuntime bool
}

func Discover(runtime bool) (Inventory, error) {
	return Discoverer{Root: "/", NVIDIARuntime: runtime}.Discover()
}

func (d Discoverer) Discover() (Inventory, error) {
	root := d.Root
	if root == "" {
		root = "/"
	}
	inv := Inventory{NVIDIARuntime: d.NVIDIARuntime}
	if _, err := os.Stat(filepath.Join(root, "proc/driver/nvidia/version")); err == nil {
		inv.NVIDIADriver = true
	}
	if groups, _ := filepath.Glob(filepath.Join(root, "sys/kernel/iommu_groups/*")); len(groups) > 0 {
		inv.IOMMU = true
	}
	entries, err := filepath.Glob(filepath.Join(root, "sys/class/drm/card[0-9]*"))
	if err != nil {
		return inv, err
	}
	byID := map[string]*Accelerator{}
	for _, card := range entries {
		device := filepath.Join(card, "device")
		vendorID := readHex(filepath.Join(device, "vendor"))
		deviceID := readHex(filepath.Join(device, "device"))
		vendor := vendorName(vendorID)
		if vendor == "" {
			continue
		}
		stable := stableDeviceID(device, vendorID, deviceID)
		a := byID[stable]
		if a == nil {
			a = &Accelerator{Vendor: vendor, VendorID: vendorID, DeviceID: deviceID, Model: modelName(vendor, deviceID), StableID: stable}
			byID[stable] = a
		}
		if node, ok := nodeInfo(root, filepath.Base(card), "card", stable); ok {
			a.Cards = append(a.Cards, node)
		}
		renders, _ := filepath.Glob(filepath.Join(card, "device/drm/renderD*"))
		for _, render := range renders {
			if node, ok := nodeInfo(root, filepath.Base(render), "render", stable); ok {
				a.RenderNodes = append(a.RenderNodes, node)
			}
		}
	}
	for _, a := range byID {
		if a.Vendor == "amd" {
			a.KFDNode, a.KFD = statNode(filepath.Join(root, "dev/kfd"), "kfd", a.StableID)
		}
		inv.Accelerators = append(inv.Accelerators, *a)
	}
	sort.Slice(inv.Accelerators, func(i, j int) bool { return inv.Accelerators[i].StableID < inv.Accelerators[j].StableID })
	return inv, nil
}

func (i Inventory) Resolve(class string) (Assignment, error) {
	if class == NVIDIAClass {
		var matches []Accelerator
		for _, a := range i.Accelerators {
			if a.Vendor == "nvidia" {
				matches = append(matches, a)
			}
		}
		if len(matches) != 1 {
			return Assignment{}, fmt.Errorf("device class %s requires exactly one unambiguous NVIDIA GPU", class)
		}
		a := matches[0]
		if i.NVIDIADriver && i.NVIDIARuntime {
			return Assignment{Class: class, Vendor: a.Vendor, StableID: a.StableID, NVIDIARuntime: true}, nil
		}
		return Assignment{}, fmt.Errorf("device class %s unavailable", class)
	}
	for _, a := range i.Accelerators {
		switch class {
		case AMDClass:
			if a.Vendor == "amd" && a.KFD && len(a.RenderNodes) > 0 {
				return Assignment{Class: class, Vendor: a.Vendor, StableID: a.StableID, Devices: []Node{a.KFDNode, a.RenderNodes[0]}}, nil
			}
		case IntelRenderClass:
			if a.Vendor == "intel" && len(a.RenderNodes) > 0 {
				return Assignment{Class: class, Vendor: a.Vendor, StableID: a.StableID, Devices: []Node{a.RenderNodes[0]}}, nil
			}
		case VAAPIClass:
			if (a.Vendor == "intel" || a.Vendor == "amd") && len(a.RenderNodes) > 0 {
				return Assignment{Class: class, Vendor: a.Vendor, StableID: a.StableID, Devices: []Node{a.RenderNodes[0]}}, nil
			}
		default:
			return Assignment{}, fmt.Errorf("unknown device class %q", class)
		}
	}
	return Assignment{}, fmt.Errorf("device class %s unavailable", class)
}

func vendorName(id string) string {
	switch id {
	case "10de":
		return "nvidia"
	case "1002":
		return "amd"
	case "8086":
		return "intel"
	}
	return ""
}
func readHex(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(string(b))), "0x")
}
func modelName(vendor, device string) string {
	if vendor == "nvidia" && device == "2571" {
		return "NVIDIA RTX A2000 12GB"
	}
	return strings.ToUpper(vendor[:1]) + vendor[1:] + " PCI device " + device
}

func (i Inventory) ModelForStableID(stableID string) string {
	for _, accelerator := range i.Accelerators {
		if accelerator.StableID == stableID {
			return accelerator.Model
		}
	}
	return ""
}
func stableDeviceID(path, vendor, device string) string {
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		if b := filepath.Base(real); strings.Contains(b, ":") {
			return b + ":" + vendor + ":" + device
		}
	}
	return vendor + ":" + device
}
func nodeInfo(root, name, kind, stable string) (Node, bool) {
	return statNode(filepath.Join(root, "dev/dri", name), kind, stable)
}
func statNode(path, kind, stable string) (Node, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return Node{}, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeDevice == 0 {
		return Node{}, false
	}
	major, minor := unix.Major(uint64(st.Rdev)), unix.Minor(uint64(st.Rdev))
	if (kind == "render" || kind == "card") && major != 226 {
		return Node{}, false
	}
	if kind == "render" && minor < 128 {
		return Node{}, false
	}
	return Node{Kind: kind, StableID: stable, Path: path, Major: major, Minor: minor}, true
}

func ParseDockerRuntimes(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m["nvidia"]
	return ok
}

func SafeView(i Inventory) map[string]any {
	items := make([]map[string]any, 0, len(i.Accelerators))
	for _, a := range i.Accelerators {
		items = append(items, map[string]any{"vendor": a.Vendor, "model": a.Model, "stable_id": a.StableID, "render_available": len(a.RenderNodes) > 0, "compute_available": a.Vendor == "nvidia" && i.NVIDIARuntime || a.Vendor == "amd" && a.KFD})
	}
	return map[string]any{"accelerators": items, "nvidia_driver": i.NVIDIADriver, "nvidia_runtime": i.NVIDIARuntime, "iommu": i.IOMMU}
}

func DevNumber(major, minor uint32) string {
	return strconv.FormatUint(uint64(major), 10) + ":" + strconv.FormatUint(uint64(minor), 10)
}
