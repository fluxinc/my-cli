package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fluxinc/my-cli/internal/contractreview"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/workspace"
)

func wantsContractReview(opts adminContractOpts, doc manifest.Document) bool {
	if opts.reviewSet {
		return opts.review
	}
	return doc.ContractReview.Enabled
}

func reviewOptions(doc manifest.Document, harness, model string) contractreview.Options {
	if harness == "" {
		harness = doc.ContractReview.Harness
	}
	if model == "" {
		model = doc.ContractReview.Model
	}
	return contractreview.Options{Harness: harness, Model: model, Timeout: time.Duration(doc.ContractReview.TimeoutSeconds) * time.Second, BudgetUSD: doc.ContractReview.MaxBudgetUSD}
}

func (a app) runContractReview(args []string) error {
	var home, name, umbrella, repo, harness, model string
	var base string
	var check, jsonOut bool
	var roots stringListFlag
	var timeout time.Duration
	var budget float64
	fs := newFlagSet("my admin contract review", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&name, "manifest", "", "registered manifest")
	fs.StringVar(&umbrella, "umbrella", "", "umbrella containing policy mounts")
	fs.StringVar(&repo, "manifest-dir", "", "explicit manifest checkout")
	fs.StringVar(&harness, "harness", "", "review harness: claude or codex")
	fs.StringVar(&model, "model", "", "explicit review model")
	fs.StringVar(&base, "base", "HEAD", "committed base for the pending diff (CI: PR base commit)")
	fs.Var(&roots, "policy-root", "committed policy source MOUNT=DIR (repeatable)")
	fs.DurationVar(&timeout, "timeout", 0, "review timeout (default 120s, maximum 10m)")
	fs.Float64Var(&budget, "max-budget-usd", 0, "maximum review cost (default 1, maximum 10)")
	fs.BoolVar(&check, "check", false, "check saved evidence without calling a model or writing")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "manifest": true, "umbrella": true, "manifest-dir": true, "harness": true, "model": true, "policy-root": true, "timeout": true, "max-budget-usd": true, "base": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("contract review accepts no positional arguments")
	}
	if repo != "" && name != "" {
		return fmt.Errorf("--manifest-dir and --manifest are mutually exclusive")
	}
	if repo == "" {
		name, err = defaultManifestName(home, name, umbrella)
		if err != nil {
			return err
		}
		doc, err := loadSingleRegisteredDoc(home, name)
		if err != nil {
			return err
		}
		repo = doc.ref.LocalPath
	}
	doc, path, err := manifest.LoadDocument(repo)
	if err != nil {
		return err
	}
	repo = filepath.Dir(path)
	contract, err := manifest.LoadContract(repo, doc)
	if err != nil {
		return err
	}
	head, err := gitPRText(repo, nil, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("review needs a committed manifest base: %w", err)
	}
	baseHead, err := gitPRText(repo, nil, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return fmt.Errorf("review diff base unavailable: %w", err)
	}
	in := collectContractReviewInput(home, name, umbrella, doc, head, string(contract.Bytes()), committedContractText(repo, baseHead), roots)
	opts := reviewOptions(doc, harness, model)
	if timeout != 0 {
		opts.Timeout = timeout
	}
	if budget != 0 {
		opts.BudgetUSD = budget
	}
	var report contractreview.Report
	if check {
		report = checkContractReview(home, repo, in, opts)
	} else {
		report = contractreview.Run(in, opts, a.contractReviewRunner)
		// A long review must not claim freshness after concurrent source edits.
		if current, _, err := manifest.LoadDocument(repo); err == nil {
			if c, err := manifest.LoadContract(repo, current); err == nil {
				h, _ := gitPRText(repo, nil, "rev-parse", "HEAD^{commit}")
				next := collectContractReviewInput(home, name, umbrella, current, h, string(c.Bytes()), "", roots)
				nextOpts := reviewOptions(current, harness, model)
				report = contractreview.Check(report, contractreview.Bind(next, nextOpts.Harness, nextOpts.Model))
				if len(next.Missing) != 0 {
					report.Status, report.Message = "incomplete", strings.Join(next.Missing, "; ")
				}
			} else {
				report.Status, report.Message = "stale", "Contract became unreadable during review."
			}
		} else {
			report.Status, report.Message = "stale", "Manifest became unreadable during review."
		}
		if err := contractreview.Save(home, repo, report); err != nil {
			return fmt.Errorf("save review evidence: %w", err)
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, report); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(a.stdout, contractreview.Markdown(report))
	}
	if report.Status != "clean" && report.Status != "findings" {
		return exitCodeError{code: 1}
	}
	return nil
}

func collectContractReviewInput(home, name, umbrella string, doc manifest.Document, head, text, previous string, roots []string) contractreview.Input {
	in := contractreview.Input{Head: head, Contract: text, Diff: contractLineDiff(previous, text), Policies: []contractreview.Policy{}}
	paths := map[string]string{}
	for _, raw := range roots {
		id, path, ok := strings.Cut(raw, "=")
		if !ok || id == "" || path == "" {
			in.Missing = append(in.Missing, "invalid --policy-root; expected MOUNT=DIR")
			continue
		}
		paths[id] = path
	}
	if name != "" && len(doc.Governance.Policies) != 0 {
		if root, err := resolveMyRoot(home, name, umbrella); err == nil {
			if mounts, err := workspace.ListMounts(home, name, root); err == nil {
				for _, mount := range mounts {
					if paths[mount.ID] == "" {
						paths[mount.ID] = mount.LocalPath
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, policy := range doc.Governance.Policies {
		if policy.ID == "" || seen[policy.ID] {
			in.Missing = append(in.Missing, "empty or duplicate policy id: "+policy.ID)
		}
		seen[policy.ID] = true
		p := contractreview.Policy{ID: policy.ID, Digest: policy.SHA256, Roles: policy.Roles}
		path := paths[policy.Mount]
		if path == "" {
			in.Missing = append(in.Missing, policy.ID+": policy mount not materialized or supplied")
		} else {
			data, err := gitPolicyBytes(path, "cat-file", "blob", "HEAD:"+filepath.ToSlash(policy.Path))
			if err != nil {
				in.Missing = append(in.Missing, policy.ID+": committed policy blob unavailable")
			} else if contractreview.Digest(data) != policy.SHA256 {
				in.Missing = append(in.Missing, policy.ID+": committed policy digest does not match manifest")
			} else if !utf8.Valid(data) {
				// JSON encoding would silently replace invalid bytes, so the
				// model would not see the exact verified policy text.
				in.Missing = append(in.Missing, policy.ID+": committed policy is not valid UTF-8")
			} else {
				p.Text = string(data)
			}
		}
		in.Policies = append(in.Policies, p)
	}
	return in
}

func checkContractReview(home, repo string, in contractreview.Input, opts contractreview.Options) contractreview.Report {
	report, err := contractreview.Load(home, repo)
	if err != nil {
		report = contractreview.NewReport(in, opts.Harness, opts.Model)
		report.Message = "No saved advisory review; run my admin contract review with an explicit harness and model."
		if !errors.Is(err, os.ErrNotExist) {
			report.Status, report.Message = "incomplete", "Cannot read saved review evidence: "+err.Error()
		}
		return report
	}
	// If no selection is configured, check against the explicit selection
	// recorded for that review. Supplying new flags still invalidates it.
	if opts.Harness == "" {
		opts.Harness = report.Evidence.Harness
	}
	if opts.Model == "" {
		opts.Model = report.Evidence.Model
	}
	report = contractreview.Check(report, contractreview.Bind(in, opts.Harness, opts.Model))
	if len(in.Missing) != 0 {
		report.Status, report.Message = "incomplete", strings.Join(in.Missing, "; ")
	}
	return report
}

func committedContractText(repo, head string) string {
	data, err := gitPRBytes(repo, nil, "cat-file", "blob", head+":manifest.json")
	if err != nil {
		return ""
	}
	var doc manifest.Document
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	var fileData []byte
	var readErr error
	if file := doc.ContractPath(); file != "" && manifest.ValidContractFilePath(file) {
		fileData, readErr = gitPRBytes(repo, nil, "cat-file", "blob", head+":"+file)
	}
	c, err := manifest.ResolveContractBytes(doc, fileData, readErr)
	if err != nil {
		return ""
	}

	return string(c.Bytes())
}

func currentContractText(root string, doc manifest.Document, edit contractEdit) string {
	if doc.ContractPath() != "" {
		c, err := manifest.ResolveContractBytes(doc, edit.originals[doc.ContractPath()], nil)
		if err != nil {
			return ""
		}
		return string(c.Bytes())
	}
	c, _ := manifest.LoadContract("", doc)
	return string(c.Bytes())
}

func (a app) reviewContractEdit(opts adminContractOpts, doc manifest.Document, repo, head string, contract manifest.Contract, previous, umbrella string) contractreview.Report {
	name := opts.manifestName
	if opts.manifestDir == "" {
		name, _ = defaultManifestName(opts.home, name, umbrella)
	}
	in := collectContractReviewInput(opts.home, name, umbrella, doc, head, string(contract.Bytes()), previous, opts.policyRoots)
	selection := reviewOptions(doc, opts.reviewHarness, opts.reviewModel)
	report := contractreview.NewReport(in, selection.Harness, selection.Model)
	if head == "" {
		report.Status, report.Message = "incomplete", "No committed manifest base is available to bind review evidence."
	} else {
		report = contractreview.Run(in, selection, a.contractReviewRunner)
	}
	if err := contractreview.Save(opts.home, repo, report); err != nil {
		report.Message += " Review evidence could not be saved: " + err.Error()
	}
	return report
}

func doctorContractReview(home string, ref manifest.Ref, doc manifest.Document, root string) []doctorItem {
	if _, err := contractreview.Load(home, ref.LocalPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	c, err := manifest.LoadContract(ref.LocalPath, doc)
	if err != nil {
		return nil
	}
	head, _ := gitPRText(ref.LocalPath, nil, "rev-parse", "HEAD^{commit}")
	in := collectContractReviewInput(home, ref.Name, root, doc, head, string(c.Bytes()), "", nil)
	report := checkContractReview(home, ref.LocalPath, in, reviewOptions(doc, "", ""))
	if report.Status == "clean" {
		return nil
	}
	return []doctorItem{{Name: ref.Name + ":contract-review", Status: "warning", Message: report.Status + ": " + report.Message + " " + contractreview.Disclaimer}}
}
