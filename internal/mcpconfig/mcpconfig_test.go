package mcpconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fluxinc/our-ai/internal/manifest"
)

func stdioService() manifest.Service {
	return manifest.Service{
		ID:      "docs-search",
		Kind:    "mcp",
		Purpose: "Search docs",
		AuthRef: "env://ACME_DOCS_TOKEN",
		Connection: manifest.ServiceConnection{
			Type:    "stdio",
			Command: "acme-docs-mcp",
			Args:    []string{"--stdio"},
			Env:     map[string]string{"ACME_DOCS_TOKEN": "${ACME_DOCS_TOKEN}"},
		},
	}
}

func readServers(t *testing.T, root string) map[string]map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse .mcp.json: %v in:\n%s", err, data)
	}
	return doc.Servers
}

func TestEnsureWritesStdioServer(t *testing.T) {
	root := t.TempDir()
	res, err := Ensure(root, t.TempDir(), []manifest.Service{stdioService()}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "created" {
		t.Fatalf("status = %q (%s)", res.Status, res.Message)
	}
	servers := readServers(t, root)
	entry, ok := servers["docs-search"]
	if !ok {
		t.Fatalf("missing docs-search entry: %v", servers)
	}
	if entry["command"] != "acme-docs-mcp" || entry["type"] != "stdio" {
		t.Fatalf("entry = %v", entry)
	}
	env, _ := entry["env"].(map[string]any)
	if env["ACME_DOCS_TOKEN"] != "${ACME_DOCS_TOKEN}" {
		t.Fatalf("env placeholder lost: %v", entry)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	root := t.TempDir()
	manifestRoot := t.TempDir()
	if _, err := Ensure(root, manifestRoot, []manifest.Service{stdioService()}, false); err != nil {
		t.Fatal(err)
	}
	res, err := Ensure(root, manifestRoot, []manifest.Service{stdioService()}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" {
		t.Fatalf("second ensure status = %q (%s)", res.Status, res.Message)
	}
}

func TestEnsureSkipsWhenNothingToMaterialize(t *testing.T) {
	root := t.TempDir()
	httpOnly := manifest.Service{ID: "status-api", Kind: "http", Purpose: "Status", AuthRef: "none", DescribeRef: "https://status.example/openapi.json"}
	res, err := Ensure(root, t.TempDir(), []manifest.Service{httpOnly}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "skipped" {
		t.Fatalf("status = %q (%s)", res.Status, res.Message)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatalf(".mcp.json should not exist: %v", err)
	}
}

func TestEnsureRefusesForeignFileWithoutForce(t *testing.T) {
	root := t.TempDir()
	foreign := []byte("{\n  \"mcpServers\": {\"mine\": {\"command\": \"keep-me\"}}\n}\n")
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Ensure(root, t.TempDir(), []manifest.Service{stdioService()}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "blocked" {
		t.Fatalf("status = %q (%s)", res.Status, res.Message)
	}
	data, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(foreign) {
		t.Fatalf("foreign .mcp.json was modified:\n%s", data)
	}

	res, err = Ensure(root, t.TempDir(), []manifest.Service{stdioService()}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "updated" {
		t.Fatalf("forced status = %q (%s)", res.Status, res.Message)
	}
	if _, ok := readServers(t, root)["docs-search"]; !ok {
		t.Fatal("forced write missing docs-search")
	}
}

func TestEnsureOverwritesOwnPreviousOutput(t *testing.T) {
	root := t.TempDir()
	manifestRoot := t.TempDir()
	if _, err := Ensure(root, manifestRoot, []manifest.Service{stdioService()}, false); err != nil {
		t.Fatal(err)
	}
	changed := stdioService()
	changed.Connection.Command = "acme-docs-mcp-v2"
	res, err := Ensure(root, manifestRoot, []manifest.Service{changed}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "updated" {
		t.Fatalf("status = %q (%s)", res.Status, res.Message)
	}
	if readServers(t, root)["docs-search"]["command"] != "acme-docs-mcp-v2" {
		t.Fatal("update not applied")
	}
}

func TestEnsureMaterializesFromLocalDescriptor(t *testing.T) {
	root := t.TempDir()
	manifestRoot := t.TempDir()
	descriptor := `{
  "name": "docs-search",
  "type": "stdio",
  "command": "acme-docs-mcp",
  "args": ["--stdio"]
}`
	if err := os.MkdirAll(filepath.Join(manifestRoot, "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestRoot, "services", "docs-search.server.json"), []byte(descriptor), 0o644); err != nil {
		t.Fatal(err)
	}
	service := manifest.Service{
		ID:          "docs-search",
		Kind:        "mcp",
		Purpose:     "Search docs",
		AuthRef:     "none",
		DescribeRef: "services/docs-search.server.json",
	}
	res, err := Ensure(root, manifestRoot, []manifest.Service{service}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "created" {
		t.Fatalf("status = %q (%s)", res.Status, res.Message)
	}
	if readServers(t, root)["docs-search"]["command"] != "acme-docs-mcp" {
		t.Fatal("descriptor connection not materialized")
	}
}

func TestEnsureURLDescribeRefIsNotMaterializable(t *testing.T) {
	root := t.TempDir()
	service := manifest.Service{
		ID:          "remote-mcp",
		Kind:        "mcp",
		Purpose:     "Remote",
		AuthRef:     "none",
		DescribeRef: "https://mcp.example/server.json",
	}
	res, err := Ensure(root, t.TempDir(), []manifest.Service{service}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "skipped" {
		t.Fatalf("status = %q (%s)", res.Status, res.Message)
	}
}
