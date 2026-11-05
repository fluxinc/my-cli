// Package credcache keeps service keys in the memory of one per-user broker
// process, so a password manager is asked once rather than on every command.
// Clients fetch a missing key themselves and hand it over; the broker never
// runs a credential helper. Same-user access is the trust boundary: this is a
// lifetime cache, not isolation between processes of one user.
package credcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
)

const (
	DefaultIdle = time.Hour
	DefaultMax  = 8 * time.Hour
	// MaxLifetime caps both limits, whatever a settings file or client asks.
	MaxLifetime = 24 * time.Hour
	// ResolveTimeout bounds one credential helper run, including its prompt.
	ResolveTimeout = 2 * time.Minute
	// DenyCooldown stops a refused or failed prompt from being asked again at
	// once by every waiting command.
	DenyCooldown = 30 * time.Second

	protocolVersion = 1
	socketName      = "broker.sock"
	lockName        = "broker.lock"
	maxUnixSocket   = 100
	maxFrame        = 1 << 20
	// callTimeout bounds every broker exchange but a wait for another fetch.
	callTimeout = 5 * time.Second
	// acquireTimeout outlasts the broker's own wait, which replies first.
	acquireTimeout = ResolveTimeout + 30*time.Second
)

// Key names what a cached secret is for. Changing any field is a cache miss,
// so a rotated reference or moved URL cannot receive an old key.
type Key struct {
	ManifestName string
	Manifest     string
	Service      string
	URL          string
	Ref          string
	Account      string
}

// ID is the opaque identity sent to the broker; the reference stays with the
// client, which is the only side that resolves it.
func (k Key) ID() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{k.Manifest, k.Service, k.URL, k.Ref, k.Account}, "\n")))
	return hex.EncodeToString(sum[:])
}

// Settings are the user's cache preferences. They live beside the personal
// references and never in a manifest.
type Settings struct {
	Disabled bool   `json:"disabled,omitempty"`
	Idle     string `json:"idle,omitempty"`
	Max      string `json:"max,omitempty"`
	// Account is the 1Password account UUID to use when several are signed in.
	Account string `json:"account,omitempty"`
}

// Limits returns the effective idle and absolute lifetimes.
func (s Settings) Limits() (idle, hard time.Duration) {
	idle, hard = DefaultIdle, DefaultMax
	if d, err := time.ParseDuration(s.Idle); err == nil {
		idle = d
	}
	if d, err := time.ParseDuration(s.Max); err == nil {
		hard = d
	}
	return clampLimits(idle, hard)
}

func clampLimits(idle, hard time.Duration) (time.Duration, time.Duration) {
	if hard <= 0 || hard > MaxLifetime {
		hard = DefaultMax
	}
	if idle <= 0 || idle > hard {
		idle = min(DefaultIdle, hard)
	}
	return idle, hard
}

func configDir(home string) (string, error) {
	registry, err := manifest.RegistryPath(home)
	if err != nil {
		return "", err
	}
	return filepath.Dir(registry), nil
}

func settingsPath(home string) (string, error) {
	dir, err := configDir(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "service-auth-cache.json"), nil
}

// LoadSettings returns defaults when the user has saved nothing.
func LoadSettings(home string) (Settings, error) {
	path, err := settingsPath(home)
	if err != nil {
		return Settings{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err = json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("invalid credential cache settings at %s", path)
	}
	return settings, nil
}

// SaveSettings atomically replaces the settings file.
func SaveSettings(home string, settings Settings) error {
	path, err := settingsPath(home)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".service-auth-cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// runtimeDir picks the private directory holding the socket and lock. Unix
// socket paths are short, so a long home falls back to a per-user directory in
// /tmp; ensurePrivateDir refuses one someone else made. It is deliberately not
// TMPDIR, which harnesses change: every command must find the same broker.
func runtimeDir(home string) (string, error) {
	dir, err := configDir(home)
	if err != nil {
		return "", err
	}
	preferred := filepath.Join(dir, "credcache")
	if len(filepath.Join(preferred, socketName)) < maxUnixSocket {
		return preferred, nil
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join("/tmp", fmt.Sprintf("my-cred-%d-%s", os.Getuid(), hex.EncodeToString(sum[:6]))), nil
}

// RuntimeDir is the directory holding one home's socket and lock.
func RuntimeDir(home string) (string, error) { return runtimeDir(home) }

// request and response are one JSON line each on the broker socket.
type request struct {
	V            int           `json:"v"`
	Op           string        `json:"op"`
	Key          string        `json:"key,omitempty"`
	Service      string        `json:"service,omitempty"`
	ManifestName string        `json:"manifest_name,omitempty"`
	Lease        string        `json:"lease,omitempty"`
	Token        string        `json:"token,omitempty"`
	Generation   uint64        `json:"generation,omitempty"`
	Idle         time.Duration `json:"idle,omitempty"`
	Max          time.Duration `json:"max,omitempty"`
	NotAfter     time.Time     `json:"not_after,omitzero"`
	Message      string        `json:"message,omitempty"`
}

type response struct {
	State      string        `json:"state"`
	Token      string        `json:"token,omitempty"`
	Generation uint64        `json:"generation,omitempty"`
	Lease      string        `json:"lease,omitempty"`
	IdleUntil  time.Time     `json:"idle_until,omitzero"`
	HardUntil  time.Time     `json:"hard_until,omitzero"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	Message    string        `json:"message,omitempty"`
	Entries    []EntryStatus `json:"entries,omitempty"`
}

const (
	stateHit      = "hit"
	stateResolve  = "resolve"
	stateCooldown = "cooldown"
	stateLocked   = "locked"
	stateTimeout  = "wait_timeout"
	stateOK       = "ok"
	stateRejected = "rejected"
	stateProtocol = "protocol_mismatch"
)

// EntryStatus describes a cached key without its value.
type EntryStatus struct {
	Service      string    `json:"service"`
	ManifestName string    `json:"manifest,omitempty"`
	Generation   uint64    `json:"generation"`
	IdleUntil    time.Time `json:"idle_until"`
	HardUntil    time.Time `json:"hard_until"`
}
