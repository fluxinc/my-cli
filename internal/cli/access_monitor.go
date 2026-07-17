package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/access"
	"github.com/fluxinc/my-cli/internal/manifest"
)

const accessMonitorSchemaVersion = 1

type accessMonitorDescriptor struct {
	SchemaVersion int      `json:"schema_version"`
	Manifest      string   `json:"manifest"`
	Platform      string   `json:"platform"`
	Label         string   `json:"label"`
	InstalledAt   string   `json:"installed_at"`
	Interval      string   `json:"interval"`
	Executable    string   `json:"executable"`
	Arguments     []string `json:"arguments"`
	Artifacts     []string `json:"artifacts,omitempty"`
}

type accessMonitorHeartbeat struct {
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	Error       string `json:"error,omitempty"`
}

type accessMonitorStatus struct {
	Installed     bool                   `json:"installed"`
	Active        bool                   `json:"active"`
	Platform      string                 `json:"platform"`
	Label         string                 `json:"label,omitempty"`
	Message       string                 `json:"message"`
	Descriptor    string                 `json:"descriptor"`
	MissingFiles  []string               `json:"missing_files,omitempty"`
	LastHeartbeat accessMonitorHeartbeat `json:"last_heartbeat,omitzero"`
}

type accessStatusReport struct {
	Manifest    string                    `json:"manifest"`
	Error       string                    `json:"error,omitempty"`
	Monitor     accessMonitorStatus       `json:"monitor"`
	Targets     []accessStatusTarget      `json:"targets"`
	Quarantines []access.QuarantineRecord `json:"quarantines,omitempty"`
}

type accessStatusTarget struct {
	Kind               string `json:"kind"`
	Repository         string `json:"repository"`
	Path               string `json:"path"`
	State              string `json:"state"`
	BaselineCheckedAt  string `json:"baseline_checked_at,omitempty"`
	LastObservation    string `json:"last_observation,omitempty"`
	ReasonCode         string `json:"reason_code,omitempty"`
	ConsecutiveDenials int    `json:"consecutive_denials,omitempty"`
}

func (a app) runAccessMonitor(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing access monitor subcommand")
	}
	switch args[0] {
	case "install":
		return a.runAccessMonitorInstall(args[1:])
	case "uninstall":
		return a.runAccessMonitorUninstall(args[1:])
	case "run":
		return a.runAccessMonitorOnce(args[1:])
	default:
		return fmt.Errorf("unknown access monitor subcommand %q", args[0])
	}
}

func (a app) runAccessMonitorInstall(args []string) error {
	home, manifestName, umbrellaRoot, _, _, err := parseAccessMutationFlags("my access monitor install", a.stderr, args, false)
	if err != nil {
		return err
	}
	manifestName, err = defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return err
	}
	if !manifest.GovernanceConfigured(doc.doc.Governance) {
		return fmt.Errorf("access monitor requires a governed manifest")
	}
	interval := 5 * time.Minute
	if value := strings.TrimSpace(doc.doc.Governance.Access.CheckInterval); value != "" {
		interval, err = time.ParseDuration(value)
		if err != nil {
			return err
		}
	}
	descriptor, err := a.installAccessMonitor(home, manifestName, umbrellaRoot, interval)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "installed\t%s\t%s\n", descriptor.Platform, descriptor.Label)
	return nil
}

func (a app) installAccessMonitor(home, manifestName, umbrellaRoot string, interval time.Duration) (accessMonitorDescriptor, error) {
	homeDir, err := resolveAccessMonitorHome(home)
	if err != nil {
		return accessMonitorDescriptor{}, err
	}
	platform := a.accessPlatform
	if platform == "" {
		platform = runtime.GOOS
	}
	executable := a.accessExecutable
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return accessMonitorDescriptor{}, err
		}
	}
	label := "com.fluxinc.my-cli.access." + safeMonitorName(manifestName)
	arguments := []string{"access", "monitor", "run", "--manifest", manifestName, "--home", homeDir}
	if umbrellaRoot != "" {
		arguments = append(arguments, "--umbrella", umbrellaRoot)
	}
	descriptor := accessMonitorDescriptor{
		SchemaVersion: accessMonitorSchemaVersion, Manifest: manifestName, Platform: platform,
		Label: label, InstalledAt: time.Now().UTC().Format(time.RFC3339Nano),
		Interval: interval.String(), Executable: executable, Arguments: arguments,
	}
	switch platform {
	case "darwin":
		plist := filepath.Join(homeDir, "Library", "LaunchAgents", label+".plist")
		descriptor.Artifacts = []string{plist}
		if err := writeAccessMonitorFile(plist, []byte(launchdMonitorPlist(label, executable, arguments, interval, os.Getenv("PATH"))), 0o600); err != nil {
			return accessMonitorDescriptor{}, err
		}
		current, err := user.Current()
		if err != nil {
			return accessMonitorDescriptor{}, err
		}
		domain := "gui/" + current.Uid
		if out, err := a.runAccessMonitorCommand("launchctl", "bootstrap", domain, plist); err != nil && !containsAnyFold(string(out), "already loaded", "already bootstrapped", "service already loaded") {
			return accessMonitorDescriptor{}, fmt.Errorf("launchctl bootstrap: %s", monitorCommandMessage(out, err))
		}
		if out, err := a.runAccessMonitorCommand("launchctl", "kickstart", "-k", domain+"/"+label); err != nil {
			return accessMonitorDescriptor{}, fmt.Errorf("launchctl kickstart: %s", monitorCommandMessage(out, err))
		}
	case "linux":
		unitDir := filepath.Join(homeDir, ".config", "systemd", "user")
		service := filepath.Join(unitDir, "my-cli-access-"+safeMonitorName(manifestName)+".service")
		timer := filepath.Join(unitDir, "my-cli-access-"+safeMonitorName(manifestName)+".timer")
		descriptor.Artifacts = []string{service, timer}
		if err := writeAccessMonitorFile(service, []byte(systemdMonitorService(executable, arguments, os.Getenv("PATH"))), 0o600); err != nil {
			return accessMonitorDescriptor{}, err
		}
		if err := writeAccessMonitorFile(timer, []byte(systemdMonitorTimer(filepath.Base(service), interval)), 0o600); err != nil {
			return accessMonitorDescriptor{}, err
		}
		if out, err := a.runAccessMonitorCommand("systemctl", "--user", "daemon-reload"); err != nil {
			return accessMonitorDescriptor{}, fmt.Errorf("systemctl daemon-reload: %s", monitorCommandMessage(out, err))
		}
		if out, err := a.runAccessMonitorCommand("systemctl", "--user", "enable", "--now", filepath.Base(timer)); err != nil {
			return accessMonitorDescriptor{}, fmt.Errorf("systemctl enable timer: %s", monitorCommandMessage(out, err))
		}
	case "windows":
		minutes := int(interval.Round(time.Minute) / time.Minute)
		if minutes < 1 {
			minutes = 1
		}
		command := windowsMonitorCommand(executable, arguments)
		if out, err := a.runAccessMonitorCommand("schtasks", "/Create", "/F", "/TN", label, "/TR", command, "/SC", "MINUTE", "/MO", strconv.Itoa(minutes)); err != nil {
			return accessMonitorDescriptor{}, fmt.Errorf("schtasks create: %s", monitorCommandMessage(out, err))
		}
	default:
		return accessMonitorDescriptor{}, fmt.Errorf("access monitor is not implemented for %s", platform)
	}
	if err := saveAccessMonitorDescriptor(homeDir, descriptor); err != nil {
		return accessMonitorDescriptor{}, err
	}
	return descriptor, nil
}

func (a app) runAccessMonitorUninstall(args []string) error {
	home, manifestName, umbrellaRoot, _, _, err := parseAccessMutationFlags("my access monitor uninstall", a.stderr, args, false)
	if err != nil {
		return err
	}
	manifestName, err = defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	homeDir, err := resolveAccessMonitorHome(home)
	if err != nil {
		return err
	}
	descriptor, err := loadAccessMonitorDescriptor(homeDir, manifestName)
	if err != nil {
		return err
	}
	switch descriptor.Platform {
	case "darwin":
		current, userErr := user.Current()
		if userErr != nil {
			return userErr
		}
		_, _ = a.runAccessMonitorCommand("launchctl", "bootout", "gui/"+current.Uid+"/"+descriptor.Label)
	case "linux":
		if len(descriptor.Artifacts) >= 2 {
			_, _ = a.runAccessMonitorCommand("systemctl", "--user", "disable", "--now", filepath.Base(descriptor.Artifacts[1]))
		}
	case "windows":
		_, _ = a.runAccessMonitorCommand("schtasks", "/Delete", "/F", "/TN", descriptor.Label)
	}
	for _, artifact := range descriptor.Artifacts {
		if err := os.Remove(artifact); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if descriptor.Platform == "linux" {
		_, _ = a.runAccessMonitorCommand("systemctl", "--user", "daemon-reload")
	}
	if err := os.Remove(accessMonitorDescriptorPath(homeDir, manifestName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Fprintf(a.stdout, "uninstalled\t%s\t%s\n", descriptor.Platform, descriptor.Label)
	return nil
}

func (a app) runAccessMonitorOnce(args []string) (runErr error) {
	home, manifestName, umbrellaRoot, _, _, err := parseAccessMutationFlags("my access monitor run", a.stderr, args, false)
	if err != nil {
		return err
	}
	manifestName, err = defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	homeDir, err := resolveAccessMonitorHome(home)
	if err != nil {
		return err
	}
	heartbeat := accessMonitorHeartbeat{StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := saveAccessMonitorHeartbeat(homeDir, manifestName, heartbeat); err != nil {
		return err
	}
	defer func() {
		heartbeat.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if runErr != nil {
			heartbeat.Error = runErr.Error()
		}
		if heartbeatErr := saveAccessMonitorHeartbeat(homeDir, manifestName, heartbeat); runErr == nil && heartbeatErr != nil {
			runErr = heartbeatErr
		}
	}()
	enforceArgs := []string{"--manifest", manifestName, "--home", homeDir}
	if umbrellaRoot != "" {
		enforceArgs = append(enforceArgs, "--umbrella", umbrellaRoot)
	}
	return a.runAccessEnforce(enforceArgs)
}

func (a app) runAccessStatus(args []string) error {
	home, manifestName, umbrellaRoot, jsonOut, _, err := parseAccessMutationFlags("my access status", a.stderr, args, false)
	if err != nil {
		return err
	}
	manifestName, err = defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	inventory, err := access.LoadInventory(home)
	if err != nil {
		return err
	}
	monitor, _ := a.currentAccessMonitorStatus(home, manifestName)
	report := accessStatusReport{Manifest: manifestName, Monitor: monitor, Quarantines: inventory.Quarantines}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		report.Error = err.Error()
		return a.printAccessStatus(report, jsonOut)
	}
	targets, err := collectAccessTargets(home, doc, umbrellaRoot)
	if err != nil {
		report.Error = err.Error()
		return a.printAccessStatus(report, jsonOut)
	}
	for _, target := range targets {
		row := accessStatusTarget{Kind: target.Kind, Repository: target.Repository, Path: target.Path, State: "activation-required"}
		if entry, ok := managedInventoryEntry(inventory, target.Path); ok {
			row.State = "allowed-cached"
			if baseline, ok := newestPositiveBaseline(entry.Baselines); ok {
				row.BaselineCheckedAt = baseline.CheckedAt
				for _, progress := range entry.Revocations {
					if progress.Actor.ID != baseline.Actor.ID {
						continue
					}
					row.LastObservation = progress.LastCheckedAt
					row.ReasonCode = progress.ReasonCode
					row.ConsecutiveDenials = progress.ConsecutiveDenials
					switch {
					case progress.ConfirmedAt != "":
						row.State = "confirmed-blocked"
					case progress.LastState == access.StateDenied:
						row.State = "revocation-pending"
					case progress.LastState == access.StateUnknown:
						row.State = "unknown-blocked"
					}
				}
			}
		} else if quarantineContainsPath(inventory.Quarantines, target.Path) {
			row.State = "quarantined"
		}
		report.Targets = append(report.Targets, row)
	}
	return a.printAccessStatus(report, jsonOut)
}

func (a app) printAccessStatus(report accessStatusReport, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, report)
	}
	if report.Error != "" {
		fmt.Fprintf(a.stdout, "error\t%s\n", report.Error)
	}
	fmt.Fprintf(a.stdout, "monitor\t%t\t%t\t%s\n", report.Monitor.Installed, report.Monitor.Active, report.Monitor.Message)
	for _, target := range report.Targets {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", target.Kind, target.Repository, target.State, target.Path)
	}
	for _, quarantine := range report.Quarantines {
		fmt.Fprintf(a.stdout, "quarantine\t%s\t%s\t%s\n", quarantine.Repository.FullName, quarantine.QuarantinedAt, strings.Join(quarantine.RetentionReasons, ","))
	}
	return nil
}

func (a app) currentAccessMonitorStatus(home, manifestName string) (accessMonitorStatus, error) {
	homeDir, err := resolveAccessMonitorHome(home)
	if err != nil {
		return accessMonitorStatus{}, err
	}
	status := accessMonitorStatus{Platform: runtime.GOOS, Descriptor: accessMonitorDescriptorPath(homeDir, manifestName)}
	descriptor, err := loadAccessMonitorDescriptor(homeDir, manifestName)
	if errors.Is(err, os.ErrNotExist) {
		status.Message = "monitor is not installed; run `my access monitor install`"
		return status, nil
	}
	if err != nil {
		status.Message = err.Error()
		return status, err
	}
	status.Installed, status.Platform, status.Label = true, descriptor.Platform, descriptor.Label
	for _, artifact := range descriptor.Artifacts {
		if _, err := os.Stat(artifact); err != nil {
			status.MissingFiles = append(status.MissingFiles, artifact)
		}
	}
	if heartbeat, err := loadAccessMonitorHeartbeat(homeDir, manifestName); err == nil {
		status.LastHeartbeat = heartbeat
	}
	if len(status.MissingFiles) != 0 {
		status.Message = "monitor registration files are missing"
		return status, nil
	}
	var out []byte
	switch descriptor.Platform {
	case "darwin":
		current, userErr := user.Current()
		if userErr != nil {
			return status, userErr
		}
		out, err = a.runAccessMonitorCommand("launchctl", "print", "gui/"+current.Uid+"/"+descriptor.Label)
	case "linux":
		if len(descriptor.Artifacts) < 2 {
			status.Message = "monitor timer artifact is missing from descriptor"
			return status, nil
		}
		out, err = a.runAccessMonitorCommand("systemctl", "--user", "is-active", filepath.Base(descriptor.Artifacts[1]))
	case "windows":
		out, err = a.runAccessMonitorCommand("schtasks", "/Query", "/TN", descriptor.Label)
	default:
		status.Message = "monitor platform is unsupported"
		return status, nil
	}
	if err != nil {
		if descriptor.Platform == "linux" {
			status.Message = "systemd user monitor is unavailable; ensure a user manager is running and enable linger with `loginctl enable-linger`: " + monitorCommandMessage(out, err)
		} else {
			status.Message = "monitor is installed but not active: " + monitorCommandMessage(out, err)
		}
		return status, nil
	}
	interval, parseErr := time.ParseDuration(descriptor.Interval)
	completedAt, heartbeatErr := time.Parse(time.RFC3339Nano, status.LastHeartbeat.CompletedAt)
	if heartbeatErr != nil {
		status.Message = "monitor is registered but has no completed heartbeat"
		return status, nil
	}
	staleAfter := 3 * interval
	if staleAfter < time.Minute {
		staleAfter = time.Minute
	}
	if parseErr != nil || time.Since(completedAt) > staleAfter {
		status.Message = "monitor heartbeat is stale"
		return status, nil
	}
	status.Active = true
	status.Message = "monitor is installed, active, and recently completed"
	return status, nil
}

func (a app) runAccessMonitorCommand(name string, args ...string) ([]byte, error) {
	if a.accessMonitorRunner != nil {
		return a.accessMonitorRunner(name, args...)
	}
	return exec.Command(name, args...).CombinedOutput()
}

func saveAccessMonitorDescriptor(home string, descriptor accessMonitorDescriptor) error {
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return err
	}
	return writeAccessMonitorFile(accessMonitorDescriptorPath(home, descriptor.Manifest), append(data, '\n'), 0o600)
}

func loadAccessMonitorDescriptor(home, manifestName string) (accessMonitorDescriptor, error) {
	data, err := os.ReadFile(accessMonitorDescriptorPath(home, manifestName))
	if err != nil {
		return accessMonitorDescriptor{}, err
	}
	var descriptor accessMonitorDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return accessMonitorDescriptor{}, err
	}
	if descriptor.SchemaVersion != accessMonitorSchemaVersion || descriptor.Manifest != manifestName {
		return accessMonitorDescriptor{}, fmt.Errorf("invalid access monitor descriptor")
	}
	return descriptor, nil
}

func saveAccessMonitorHeartbeat(home, manifestName string, heartbeat accessMonitorHeartbeat) error {
	data, err := json.MarshalIndent(heartbeat, "", "  ")
	if err != nil {
		return err
	}
	return writeAccessMonitorFile(accessMonitorHeartbeatPath(home, manifestName), append(data, '\n'), 0o600)
}

func loadAccessMonitorHeartbeat(home, manifestName string) (accessMonitorHeartbeat, error) {
	data, err := os.ReadFile(accessMonitorHeartbeatPath(home, manifestName))
	if err != nil {
		return accessMonitorHeartbeat{}, err
	}
	var heartbeat accessMonitorHeartbeat
	if err := json.Unmarshal(data, &heartbeat); err != nil {
		return accessMonitorHeartbeat{}, err
	}
	return heartbeat, nil
}

func writeAccessMonitorFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".monitor-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return os.Chmod(path, mode)
}

func accessMonitorDescriptorPath(home, manifestName string) string {
	return filepath.Join(home, ".local", "state", "my-cli", "access", "monitors", safeMonitorName(manifestName)+".json")
}

func accessMonitorHeartbeatPath(home, manifestName string) string {
	return filepath.Join(home, ".local", "state", "my-cli", "access", "monitors", safeMonitorName(manifestName)+".heartbeat.json")
}

func resolveAccessMonitorHome(home string) (string, error) {
	if home == "" {
		return os.UserHomeDir()
	}
	return filepath.Abs(home)
}

func launchdMonitorPlist(label, executable string, arguments []string, interval time.Duration, pathEnvironment string) string {
	seconds := int(interval.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	var args strings.Builder
	for _, value := range append([]string{executable}, arguments...) {
		fmt.Fprintf(&args, "    <string>%s</string>\n", html.EscapeString(value))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array>
%s  </array>
  <key>RunAtLoad</key><true/>
  <key>StartInterval</key><integer>%d</integer>
  <key>EnvironmentVariables</key><dict><key>PATH</key><string>%s</string></dict>
</dict></plist>
`, html.EscapeString(label), args.String(), seconds, html.EscapeString(pathEnvironment))
}

func systemdMonitorService(executable string, arguments []string, pathEnvironment string) string {
	parts := []string{systemdQuote(executable)}
	for _, argument := range arguments {
		parts = append(parts, systemdQuote(argument))
	}
	return "[Unit]\nDescription=My CLI governed repository access check\n\n[Service]\nType=oneshot\nEnvironment=" + systemdQuote("PATH="+pathEnvironment) + "\nExecStart=" + strings.Join(parts, " ") + "\n"
}

func systemdMonitorTimer(service string, interval time.Duration) string {
	return fmt.Sprintf("[Unit]\nDescription=Run My CLI governed repository access checks\n\n[Timer]\nOnBootSec=30s\nOnUnitActiveSec=%s\nUnit=%s\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n", interval.String(), service)
}

func systemdQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func windowsMonitorCommand(executable string, arguments []string) string {
	parts := []string{strconv.Quote(executable)}
	for _, argument := range arguments {
		parts = append(parts, strconv.Quote(argument))
	}
	return strings.Join(parts, " ")
}

func safeMonitorName(value string) string {
	var result strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			result.WriteRune(r)
		}
	}
	if result.Len() == 0 {
		return "manifest"
	}
	return result.String()
}

func quarantineContainsPath(records []access.QuarantineRecord, path string) bool {
	abs, _ := filepath.Abs(path)
	for _, record := range records {
		for _, source := range record.Sources {
			if source.OriginalPath == abs || source.OriginalPath == path {
				return true
			}
		}
	}
	return false
}

func containsAnyFold(value string, needles ...string) bool {
	value = strings.ToLower(value)
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func monitorCommandMessage(out []byte, err error) string {
	if value := strings.TrimSpace(string(out)); value != "" {
		return value
	}
	return err.Error()
}
