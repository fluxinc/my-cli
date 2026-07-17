package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAccessMonitorInstallStatusAndUninstallAreIdempotentAcrossPlatforms(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			home, _, _, _, _ := setupCLITrackedManifestBody(t, governedAccessTestManifest())
			var calls []string
			runner := func(name string, args ...string) ([]byte, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				return []byte("active"), nil
			}
			var stdout, stderr bytes.Buffer
			a := app{
				stdout: &stdout, stderr: &stderr, accessMonitorRunner: runner,
				accessPlatform: platform, accessExecutable: filepath.Join(home, "bin", "my"),
			}
			args := []string{"my", "access", "monitor", "install", "--manifest", "acme", "--home", home}
			if err := a.run(args); err != nil {
				t.Fatal(err)
			}
			if err := a.run(args); err != nil {
				t.Fatalf("second install was not idempotent: %v", err)
			}
			descriptor, err := loadAccessMonitorDescriptor(home, "acme")
			if err != nil {
				t.Fatal(err)
			}
			if descriptor.Platform != platform || descriptor.Manifest != "acme" {
				t.Fatalf("descriptor = %#v", descriptor)
			}
			for _, artifact := range descriptor.Artifacts {
				if _, err := os.Stat(artifact); err != nil {
					t.Fatalf("monitor artifact missing: %v", err)
				}
			}
			if err := saveAccessMonitorHeartbeat(home, "acme", accessMonitorHeartbeat{
				StartedAt:   time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano),
				CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
			}); err != nil {
				t.Fatal(err)
			}
			status, err := a.currentAccessMonitorStatus(home, "acme")
			if err != nil {
				t.Fatal(err)
			}
			if !status.Installed || !status.Active || len(status.MissingFiles) != 0 {
				t.Fatalf("status = %#v", status)
			}
			if len(calls) == 0 {
				t.Fatal("monitor install/status made no platform service-manager calls")
			}
			if err := a.run([]string{"my", "access", "monitor", "uninstall", "--manifest", "acme", "--home", home}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(accessMonitorDescriptorPath(home, "acme")); !os.IsNotExist(err) {
				t.Fatalf("monitor descriptor remained after uninstall: %v", err)
			}
			for _, artifact := range descriptor.Artifacts {
				if _, err := os.Stat(artifact); !os.IsNotExist(err) {
					t.Fatalf("monitor artifact remained after uninstall: %s err=%v", artifact, err)
				}
			}
		})
	}
}

func TestAccessMonitorRunRecordsCompletedHeartbeat(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, governedAccessTestManifest())
	materializeAccessTargetsForTest(t, home)
	a := testAccessMonitorApp(app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAccessRunner(false)}, home)
	if err := a.run([]string{"my", "access", "activate", "--yes", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if err := a.run([]string{"my", "access", "monitor", "run", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	heartbeat, err := loadAccessMonitorHeartbeat(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.StartedAt == "" || heartbeat.CompletedAt == "" || heartbeat.Error != "" {
		t.Fatalf("heartbeat = %#v", heartbeat)
	}
}

func TestDoctorReportsMissingAndStaleGovernedAccessMonitor(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, governedAccessTestManifest())
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessMonitorRunner: func(name string, args ...string) ([]byte, error) {
		return []byte("active"), nil
	}}
	report := a.buildDoctorReport(home, "acme", "", doctorOptions{NoFetch: true})
	if len(report.Access) != 1 || report.Access[0].Status != "error" || !strings.Contains(report.Access[0].Message, "not installed") {
		t.Fatalf("missing monitor doctor report = %#v", report.Access)
	}
	a.accessPlatform = "linux"
	a.accessExecutable = filepath.Join(home, "bin", "my")
	if _, err := a.installAccessMonitor(home, "acme", "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := saveAccessMonitorHeartbeat(home, "acme", accessMonitorHeartbeat{
		StartedAt:   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		CompletedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	report = a.buildDoctorReport(home, "acme", "", doctorOptions{NoFetch: true})
	if len(report.Access) != 1 || report.Access[0].Status != "error" || !strings.Contains(report.Access[0].Message, "stale") {
		t.Fatalf("stale monitor doctor report = %#v", report.Access)
	}
	if err := saveAccessMonitorHeartbeat(home, "acme", accessMonitorHeartbeat{
		StartedAt:   time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano),
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	report = a.buildDoctorReport(home, "acme", "", doctorOptions{NoFetch: true})
	if len(report.Access) != 1 || report.Access[0].Status != "ok" {
		t.Fatalf("healthy monitor doctor report = %#v", report.Access)
	}
}

func TestAccessMonitorFilesUseRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not Windows ACLs")
	}
	home, _, _, _, _ := setupCLITrackedManifestBody(t, governedAccessTestManifest())
	a := app{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessPlatform: "linux",
		accessExecutable:    filepath.Join(home, "bin", "my"),
		accessMonitorRunner: func(name string, args ...string) ([]byte, error) { return nil, nil },
	}
	descriptor, err := a.installAccessMonitor(home, "acme", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	paths := append([]string{accessMonitorDescriptorPath(home, "acme")}, descriptor.Artifacts...)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
}

func TestAccessMonitorStatusExplainsSystemdLingerRequirement(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, governedAccessTestManifest())
	a := app{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessPlatform: "linux",
		accessExecutable:    filepath.Join(home, "bin", "my"),
		accessMonitorRunner: func(name string, args ...string) ([]byte, error) { return nil, nil },
	}
	if _, err := a.installAccessMonitor(home, "acme", "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := saveAccessMonitorHeartbeat(home, "acme", accessMonitorHeartbeat{
		StartedAt:   time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano),
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	a.accessMonitorRunner = func(name string, args ...string) ([]byte, error) {
		return []byte("Failed to connect to bus: No medium found"), os.ErrNotExist
	}
	status, err := a.currentAccessMonitorStatus(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if status.Active || !strings.Contains(status.Message, "loginctl enable-linger") {
		t.Fatalf("systemd status = %#v", status)
	}
}
