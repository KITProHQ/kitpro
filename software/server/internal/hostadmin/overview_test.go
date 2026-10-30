package hostadmin

import (
	"strings"
	"testing"
)

func TestParseMemoryRequiresTotal(t *testing.T) {
	if _, err := parseMemory("MemAvailable: 10 kB\n"); err == nil {
		t.Fatal("missing total accepted")
	}
	got, err := parseMemory("MemTotal: 100 kB\nMemAvailable: 50 kB\nSwapTotal: 20 kB\nSwapFree: 10 kB\n")
	if err != nil || got.TotalBytes != 102400 || got.AvailableBytes != 51200 || got.SwapFreeBytes != 10240 {
		t.Fatalf("memory parse: %#v, %v", got, err)
	}
}
func TestCollectNeverInventsHealthyState(t *testing.T) {
	got := Collect()
	if got.Architecture == "" || got.Collectors == nil {
		t.Fatal("missing overview metadata")
	}
	for name, status := range got.Collectors {
		if status.State != Available && status.State != Unavailable && status.State != NotConfigured {
			t.Fatalf("%s has invalid state %q", name, status.State)
		}
	}
	if got.Systemd.State == "healthy" || got.Security.State == "healthy" {
		t.Fatal("unverified host facility claimed healthy")
	}
}
func TestCPUTicksRejectsMalformedInput(t *testing.T) {
	if _, _, err := cpuTicks("cpu nonsense\n"); err == nil {
		t.Fatal("invalid counters accepted")
	}
	total, idle, err := cpuTicks("cpu 10 0 5 20 3 0 0 0\n")
	if err != nil || total != 38 || idle != 23 {
		t.Fatalf("CPU ticks: %d %d %v", total, idle, err)
	}
}

func TestParseLinuxReleaseInstalledProductionGeneration(t *testing.T) {
	packageMetadata := `{"id":"kitpro-linux","release":"1.0","generation_policy":"current","support_status":"release-candidate-preparation"}`
	installedMetadata := `{"id":"kitpro-linux","release":"1.0","generation":"1.0-g0002"}`
	release, err := parseLinuxRelease(packageMetadata, installedMetadata)
	if err != nil || release.ID != "kitpro-linux" || release.Release != "1.0" || release.Generation != "1.0-g0002" || release.SupportStatus != "release-candidate-preparation" {
		t.Fatalf("production release: %#v, %v", release, err)
	}
}

func TestParseLinuxReleaseKeepsDevelopmentGeneration(t *testing.T) {
	release, err := parseLinuxRelease(`{"id":"kitpro-linux","release":"0.1-dev","generation":"0.1-dev-g0003"}`, "")
	if err != nil || release.Generation != "0.1-dev-g0003" {
		t.Fatalf("development release: %#v, %v", release, err)
	}
}

func TestParseLinuxReleaseRejectsMissingOrMismatchedInstalledIdentity(t *testing.T) {
	packageMetadata := `{"id":"kitpro-linux","release":"1.0","generation_policy":"current"}`
	for name, installed := range map[string]string{
		"missing":         "",
		"wrong release":   `{"id":"kitpro-linux","release":"0.1-dev","generation":"1.0-g0002"}`,
		"wrong ID":        `{"id":"other-linux","release":"1.0","generation":"1.0-g0002"}`,
		"no generation":   `{"id":"kitpro-linux","release":"1.0"}`,
		"invalid payload": `{"id":"kitpro-linux",`,
		"long generation": `{"id":"kitpro-linux","release":"1.0","generation":"` + strings.Repeat("x", 129) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if release, err := parseLinuxRelease(packageMetadata, installed); err == nil || release.Generation != "" {
				t.Fatalf("accepted invalid installed identity: %#v, %v", release, err)
			}
		})
	}
}

func TestParseLinuxReleaseRejectsUnboundedOrUnsupportedMetadata(t *testing.T) {
	for name, pkg := range map[string]string{
		"unknown policy":  `{"id":"kitpro-linux","release":"1.0","generation_policy":"unknown","generation":"g0002"}`,
		"wrong ID":        `{"id":"other-linux","release":"1.0","generation":"g0002"}`,
		"long generation": `{"id":"kitpro-linux","release":"1.0","generation":"` + strings.Repeat("x", 129) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseLinuxRelease(pkg, ""); err == nil {
				t.Fatal("accepted invalid package metadata")
			}
		})
	}
}

func TestParseLinuxReleaseCurrentPolicyUsesInstalledGeneration(t *testing.T) {
	pkg := `{"id":"kitpro-linux","release":"1.0","generation_policy":"current","generation":"stale"}`
	installed := `{"id":"kitpro-linux","release":"1.0","generation":"1.0-g0002"}`
	release, err := parseLinuxRelease(pkg, installed)
	if err != nil || release.Generation != "1.0-g0002" {
		t.Fatalf("current policy used stale package value: %#v, %v", release, err)
	}
}
