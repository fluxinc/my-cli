package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/contractreview"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func reviewTestReply(findings string) []byte {
	data, _ := json.Marshal(map[string]any{"subtype": "success", "result": findings})
	return data
}

func TestContractReviewPolicyVerificationAndReadOnlyCheck(t *testing.T) {
	home, repo, policyRepo := t.TempDir(), t.TempDir(), t.TempDir()
	policyText := "Policy full text.\nOnly reviewed changes are allowed.\n"
	writeCLITestFile(t, filepath.Join(policyRepo, "policy.md"), policyText)
	initCLIGitRepo(t, policyRepo)
	doc := manifest.Document{ManifestVersion: 1, Organization: manifest.Organization{ID: "acme", Name: "Acme"}, Contract: []string{"Review changes."}, Governance: manifest.Governance{Policies: []manifest.Policy{{ID: "review", Mount: "handbook", Path: "policy.md", SHA256: contractreview.Digest([]byte(policyText))}}}}
	data, _ := json.Marshal(doc)
	writeCLITestFile(t, filepath.Join(repo, "manifest.json"), string(data))
	initCLIGitRepo(t, repo)
	before, _ := os.ReadFile(filepath.Join(repo, "manifest.json"))
	calls := 0
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}, contractReviewRunner: func(_ context.Context, _ []string, prompt string) ([]byte, error) {
		calls++
		if !strings.Contains(prompt, "Only reviewed changes are allowed.") {
			t.Fatal("full policy omitted")
		}
		return reviewTestReply(`{"findings":[]}`), nil
	}}
	args := []string{"--manifest-dir", repo, "--home", home, "--harness", "claude", "--model", "chosen", "--json"}
	if err := a.runContractReview(args); err == nil {
		t.Fatal("missing policy was clean")
	}
	if calls != 0 {
		t.Fatal("missing policy invoked model")
	}
	args = append(args, "--policy-root", "handbook="+policyRepo)
	out.Reset()
	if err := a.runContractReview(args); err != nil {
		t.Fatal(err)
	}
	var report contractreview.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Status != "clean" {
		t.Fatalf("%v %s", err, out)
	}
	path, _ := contractreview.Path(home, repo)
	evidence, _ := os.ReadFile(path)
	out.Reset()
	if err := a.runContractReview(append(args, "--check")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("check invoked model")
	}
	afterEvidence, _ := os.ReadFile(path)
	after, _ := os.ReadFile(filepath.Join(repo, "manifest.json"))
	if !bytes.Equal(before, after) || !bytes.Equal(evidence, afterEvidence) {
		t.Fatal("review/check mutated source or check mutated evidence")
	}
	// A policy-only update invalidates the saved evidence, even with the same
	// contract bytes and unchanged manifest HEAD.
	newPolicy := "New policy text.\n"
	writeCLITestFile(t, filepath.Join(policyRepo, "policy.md"), newPolicy)
	runCLIGit(t, policyRepo, "add", ".")
	runCLIGit(t, policyRepo, "commit", "-qm", "Update policy")
	doc.Governance.Policies[0].SHA256 = contractreview.Digest([]byte(newPolicy))
	data, _ = json.Marshal(doc)
	writeCLITestFile(t, filepath.Join(repo, "manifest.json"), string(data))
	out.Reset()
	if err := a.runContractReview(append(args, "--check")); err == nil {
		t.Fatal("policy-only change not stale")
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Status != "stale" {
		t.Fatalf("%v %s", err, out)
	}
	if calls != 1 {
		t.Fatal("staleness check invoked model")
	}
}

func TestContractReviewAuthoringIsOptInAndFindingsDoNotBlockPR(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default-off", true: "explicit-review"}[enabled], func(t *testing.T) {
			body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"umbrella":{"recommended_path":"~/acme"},"governance":{"authorization":{"provider":"github","manifest_repository":"example/control","admin_permission":"admin"}}}`
			home, root, cache, remote, _ := setupCLITrackedManifestBody(t, body)
			if _, _, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
				t.Fatal(err)
			}
			reg, _ := manifest.LoadRegistry(home)
			reg.Manifests[0].GitURL = "https://github.com/example/control.git"
			if err := manifest.SaveRegistry(home, reg); err != nil {
				t.Fatal(err)
			}
			state := &governedPRRunnerState{remote: remote, permission: "admin", repository: "example/control"}
			calls := 0
			prBody := ""
			out := &bytes.Buffer{}
			a := app{stdout: out, stderr: &bytes.Buffer{}, accessRunner: governedAdminAccessRunner(), publishRunner: func(name string, args ...string) ([]byte, error) {
				for i, arg := range args {
					if arg == "--body" && i+1 < len(args) {
						prBody = args[i+1]
					}
				}
				return state.run(name, args...)
			}, contractReviewRunner: func(_ context.Context, _ []string, prompt string) ([]byte, error) {
				calls++
				if !strings.Contains(prompt, "Proposed rule.") {
					t.Fatal("candidate missing")
				}
				return reviewTestReply(`{"findings":[{"kind":"vague","message":"Needs specificity","remove":"Proposed rule.","add":"Specific rule."}]}`), nil
			}}
			before, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
			args := []string{"add", "Proposed rule.", "--home", home, "--umbrella", root, "--json"}
			if enabled {
				args = append(args, "--review", "--harness", "claude", "--model", "chosen")
			}
			if err := a.runAdminContract(args); err != nil {
				t.Fatal(err)
			}
			var result adminContractResult
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.PRURL == "" {
				t.Fatal("findings blocked PR")
			}
			if enabled {
				if calls != 1 || result.Review == nil || result.Review.Status != "findings" || result.Review.Evidence.Head != result.PRHeadSHA || !strings.Contains(prBody, result.PRHeadSHA) || !strings.Contains(prBody, "Conflict review") {
					t.Fatalf("bad review binding: %+v calls=%d body=%s", result, calls, prBody)
				}
			} else if calls != 0 || result.Review != nil || strings.Contains(prBody, "Conflict review") {
				t.Fatal("default authoring invoked review")
			}
			after, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("review rewrote cache")
			}
			proposal := gitCLIOutput(t, remote, "show", state.commit+":manifest.json")
			if strings.Contains(proposal, "Specific rule.") {
				t.Fatal("model suggestion was applied")
			}
		})
	}
}

func TestContractReviewManifestOptInAndDoctorPolicyOnlyStaleness(t *testing.T) {
	doc := manifest.Document{ContractReview: manifest.ContractReview{Enabled: true, Harness: "claude", Model: "chosen"}, Contract: []string{"Rule."}}
	if !wantsContractReview(adminContractOpts{}, doc) || wantsContractReview(adminContractOpts{reviewSet: true, review: false}, doc) || !wantsContractReview(adminContractOpts{reviewSet: true, review: true}, manifest.Document{}) {
		t.Fatal("review opt-in precedence")
	}
	home, repo := t.TempDir(), t.TempDir()
	data, _ := json.Marshal(doc)
	writeCLITestFile(t, filepath.Join(repo, "manifest.json"), string(data))
	initCLIGitRepo(t, repo)
	head := strings.TrimSpace(gitCLIOutput(t, repo, "rev-parse", "HEAD"))
	c, _ := manifest.LoadContract(repo, doc)
	in := contractreview.Input{Head: head, Contract: string(c.Bytes())}
	report := contractreview.NewReport(in, "claude", "chosen")
	report.Status = "clean"
	if err := contractreview.Save(home, repo, report); err != nil {
		t.Fatal(err)
	}
	ref := manifest.Ref{Name: "acme", LocalPath: repo}
	if items := doctorContractReview(home, ref, doc, ""); len(items) != 0 {
		t.Fatalf("fresh review warned: %+v", items)
	}
	doc.ContractReview.Model = "other-model"
	if items := doctorContractReview(home, ref, doc, ""); len(items) != 1 || !strings.Contains(items[0].Message, "stale") {
		t.Fatalf("stale review missing: %+v", items)
	}
	doc.Governance.Policies = []manifest.Policy{{ID: "new-policy", Mount: "absent", Path: "policy.md", SHA256: "sha256:new"}}
	if items := doctorContractReview(home, ref, doc, ""); len(items) != 1 || !strings.Contains(items[0].Message, "incomplete") {
		t.Fatalf("missing policy not reported: %+v", items)
	}
}

func TestContractReviewDetectsModelConfigurationChangedDuringReview(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	doc := manifest.Document{ManifestVersion: 1, Organization: manifest.Organization{ID: "acme", Name: "Acme"}, Contract: []string{"Rule."}, ContractReview: manifest.ContractReview{Harness: "claude", Model: "chosen"}}
	data, _ := json.Marshal(doc)
	writeCLITestFile(t, filepath.Join(repo, "manifest.json"), string(data))
	initCLIGitRepo(t, repo)
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}, contractReviewRunner: func(context.Context, []string, string) ([]byte, error) {
		doc.ContractReview.Model = "changed"
		data, _ := json.Marshal(doc)
		writeCLITestFile(t, filepath.Join(repo, "manifest.json"), string(data))
		return reviewTestReply(`{"findings":[]}`), nil
	}}
	if err := a.runContractReview([]string{"--manifest-dir", repo, "--home", home, "--json"}); err == nil {
		t.Fatal("changed model remained fresh")
	}
	var report contractreview.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Status != "stale" {
		t.Fatalf("%v %s", err, out)
	}
}

// JSON encoding would silently replace invalid bytes, so a policy that is not
// valid UTF-8 cannot be sent in full and the review stops before the model.
func TestContractReviewRejectsNonUTF8PolicyBeforeModel(t *testing.T) {
	home, repo, policyRepo := t.TempDir(), t.TempDir(), t.TempDir()
	policyText := "Policy \xff text.\n"
	writeCLITestFile(t, filepath.Join(policyRepo, "policy.md"), policyText)
	initCLIGitRepo(t, policyRepo)
	doc := manifest.Document{ManifestVersion: 1, Organization: manifest.Organization{ID: "acme", Name: "Acme"}, Contract: []string{"Review changes."}, Governance: manifest.Governance{Policies: []manifest.Policy{{ID: "review", Mount: "handbook", Path: "policy.md", SHA256: contractreview.Digest([]byte(policyText))}}}}
	data, _ := json.Marshal(doc)
	writeCLITestFile(t, filepath.Join(repo, "manifest.json"), string(data))
	initCLIGitRepo(t, repo)
	calls := 0
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}, contractReviewRunner: func(context.Context, []string, string) ([]byte, error) {
		calls++
		return reviewTestReply(`{"findings":[]}`), nil
	}}
	err := a.runContractReview([]string{"--manifest-dir", repo, "--home", home, "--harness", "claude", "--model", "chosen", "--policy-root", "handbook=" + policyRepo, "--json"})
	var report contractreview.Report
	_ = json.Unmarshal(out.Bytes(), &report)
	if err == nil || calls != 0 || report.Status != "incomplete" || !strings.Contains(report.Message, "not valid UTF-8") {
		t.Fatalf("err=%v calls=%d report=%+v", err, calls, report)
	}
}
