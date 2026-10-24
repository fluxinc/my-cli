package contractreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

func Path(home, repo string) (string, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(home, ".local", "share", "my-cli", "state", "contract-review", hex.EncodeToString(sum[:])+".json"), nil
}

func Load(home, repo string) (Report, error) {
	var report Report
	path, err := Path(home, repo)
	if err != nil {
		return report, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &report)
	}
	return report, err
}

func Save(home, repo string, report Report) error {
	path, err := Path(home, repo)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".review-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
