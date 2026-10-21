// Package serviceauth stores per-user credential references, never secrets.
package serviceauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fluxinc/my-cli/internal/manifest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Binding pins an override to the manifest identity and exact API endpoint.
// Reusing a service ID in another organization or moving its URL cannot send
// the old user's credential to that new destination.
type Binding struct {
	Manifest string
	Service  string
	URL      string
}
type Entry struct {
	Ref       string    `json:"auth_ref"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

func (b Binding) key() string {
	sum := sha256.Sum256([]byte(b.Manifest + "\n" + b.Service + "\n" + b.URL))
	return hex.EncodeToString(sum[:])
}
func Path(home string) (string, error) {
	registry, err := manifest.RegistryPath(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(registry), "service-auth.json"), nil
}
func ValidRef(ref string) bool {
	if strings.HasPrefix(ref, "env://") {
		return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(strings.TrimPrefix(ref, "env://"))
	}
	if strings.HasPrefix(ref, "op://") {
		return len(strings.TrimPrefix(ref, "op://")) > 0 && !strings.ContainsAny(ref, " \t\r\n")
	}
	return false
}
func load(home string) (map[string]Entry, error) {
	path, err := Path(home)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries map[string]Entry
	if err = json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("invalid personal service references at %s", path)
	}
	if entries == nil {
		entries = map[string]Entry{}
	}
	for _, entry := range entries {
		if !ValidRef(entry.Ref) {
			return nil, fmt.Errorf("invalid personal service reference at %s", path)
		}
	}
	return entries, nil
}
func Lookup(home string, b Binding) (Entry, bool, error) {
	entries, err := load(home)
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := entries[b.key()]
	return e, ok, nil
}
func Set(home string, b Binding, e Entry) error {
	if !ValidRef(e.Ref) {
		return errors.New("use an op:// or env:// reference; literal credentials are not accepted")
	}
	entries, err := load(home)
	if err != nil {
		return err
	}
	entries[b.key()] = e
	return save(home, entries)
}
func Unset(home string, b Binding) error {
	entries, err := load(home)
	if err != nil {
		return err
	}
	delete(entries, b.key())
	return save(home, entries)
}
func save(home string, entries map[string]Entry) error {
	path, err := Path(home)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".service-auth-*")
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
