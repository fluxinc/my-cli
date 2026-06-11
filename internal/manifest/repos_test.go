package manifest

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRepoCatalogReadsRepos(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, RepoCatalogPath(ref), `[
  {
    "id": "sample-service",
    "git_url": "https://github.com/acme/sample-service.git",
    "description": "Sample service source",
    "default": true
  },
  {
    "id": "infra-configs",
    "git_url": "https://github.com/acme/infra-configs.git",
    "description": "Deployment configs"
  }
]`)
	repos, err := LoadRepoCatalog(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].ID != "sample-service" || !repos[0].Default || repos[1].Default {
		t.Fatalf("repos = %#v", repos)
	}
	repo, ok, err := FindRepo(home, "acme", "infra-configs")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || repo.GitURL != "https://github.com/acme/infra-configs.git" {
		t.Fatalf("repo = %#v, ok=%v", repo, ok)
	}
}

func TestLoadRepoCatalogMissingFileIsEmpty(t *testing.T) {
	home := t.TempDir()
	if _, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git"); err != nil {
		t.Fatal(err)
	}
	repos, err := LoadRepoCatalog(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Fatalf("repos = %#v", repos)
	}
}

func TestLoadRepoCatalogRejectsDuplicateAndMissingGitURL(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, RepoCatalogPath(ref), `[
  { "id": "alpha", "git_url": "https://github.com/acme/alpha.git" },
  { "id": "alpha", "git_url": "https://github.com/acme/alpha2.git" }
]`)
	if _, err := LoadRepoCatalog(home, "acme"); err == nil || !strings.Contains(err.Error(), "duplicate repo id") {
		t.Fatalf("err = %v, want duplicate repo id", err)
	}
	writeFile(t, RepoCatalogPath(ref), `[
  { "id": "alpha" }
]`)
	if _, err := LoadRepoCatalog(home, "acme"); err == nil || !strings.Contains(err.Error(), "git_url is required") {
		t.Fatalf("err = %v, want git_url required", err)
	}
}

func TestProductsNoLongerCarryGitURLAndMayLinkRepos(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ref.LocalPath, manifestFile), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" }
}`)
	writeFile(t, RepoCatalogPath(ref), `[
  { "id": "sample-service", "git_url": "https://github.com/acme/sample-service.git" }
]`)
	writeFile(t, ProductCatalogPath(ref), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "description": "Sample service",
    "repos": ["sample-service"]
  }
]`)
	products, err := LoadCatalog(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || len(products[0].Repos) != 1 || products[0].Repos[0] != "sample-service" {
		t.Fatalf("products = %#v", products)
	}
}

func TestProductsRejectLegacyGitURL(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, ProductCatalogPath(ref), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "git_url": "https://github.com/acme/sample-product.git",
    "description": "Sample service"
  }
]`)
	_, err = LoadCatalog(home, "acme")
	if err == nil || !strings.Contains(err.Error(), "git_url") || !strings.Contains(err.Error(), "repos.json") {
		t.Fatalf("err = %v, want legacy git_url error naming repos.json migration", err)
	}
}

func TestProductsRejectUnknownRepoLink(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ref.LocalPath, manifestFile), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" }
}`)
	writeFile(t, ProductCatalogPath(ref), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "description": "Sample service",
    "repos": ["nope"]
  }
]`)
	_, err = LoadCatalog(home, "acme")
	if err == nil || !strings.Contains(err.Error(), `repo "nope"`) {
		t.Fatalf("err = %v, want unknown repo link error", err)
	}
}

func TestValidateFlagsLegacyProductGitURL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, manifestFile), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" }
}`)
	writeFile(t, filepath.Join(dir, "catalog", "products.json"), `[
  { "id": "p", "name": "P", "git_url": "https://github.com/acme/p.git", "description": "d" }
]`)
	result := ValidateFile(dir)
	if len(result.Errors) == 0 || !strings.Contains(strings.Join(result.Errors, "\n"), "repos.json") {
		t.Fatalf("validation errors = %#v, want legacy git_url error", result.Errors)
	}
}
