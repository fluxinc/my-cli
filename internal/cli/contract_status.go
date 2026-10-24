package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fluxinc/my-cli/internal/contractstate"
	"github.com/fluxinc/my-cli/internal/manifest"
)

type contractRootStatus struct {
	contractstate.Root
	Status string `json:"status"`
}

type contractInstanceStatus struct {
	contractstate.Instance
	Status string `json:"status"`
}

type contractStatusReport struct {
	Instances                 []contractInstanceStatus `json:"instances"`
	Manifest                  string                   `json:"manifest"`
	Active                    *contractstate.Snapshot  `json:"active,omitempty"`
	Local                     contractstate.Snapshot   `json:"local"`
	LastSuccessfulRemoteCheck string                   `json:"last_successful_remote_check,omitempty"`
	Roots                     []contractRootStatus     `json:"roots"`
	LiveDelivery              string                   `json:"live_delivery"`
}

func (a app) runContractStateCommand(action string, args []string) error {
	var home, name, root, from, to string
	var jsonOut bool
	fs := newFlagSet("my contract "+action, a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&name, "manifest", "", "select a registered manifest")
	fs.StringVar(&root, "umbrella", "", "select an umbrella root")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	if action == "diff" {
		fs.StringVar(&from, "from", "", "compare from this local Git commit")
		fs.StringVar(&to, "to", "", "compare to this local Git commit")
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "manifest": true, "umbrella": true, "from": true, "to": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my contract %s", action)
	}
	name, err = defaultManifestName(home, name, root)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	ref, ok, err := manifest.Find(home, name)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !ok {
		return fmt.Errorf("manifest %q not registered", name)
	}
	state, err := contractstate.Load(home, name)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	current, err := currentContractSnapshot(ref)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if action == "diff" {
		if to == "" && state.Active == nil && current.Configured {
			return a.maybeJSONError(jsonOut, fmt.Errorf("no active contract revision recorded; run my sync first or select --to REV"))
		}
		if to == "" && state.Active != nil {
			current = *state.Active
		}
		old := contractstate.Snapshot{}
		if state.Previous != nil {
			old = *state.Previous
		} else if state.Active != nil {
			old = *state.Active
		}
		if from != "" {
			old, err = contractSnapshotAt(ref, from)
			if err != nil {
				return a.maybeJSONError(jsonOut, err)
			}
		}
		if to != "" {
			current, err = contractSnapshotAt(ref, to)
			if err != nil {
				return a.maybeJSONError(jsonOut, err)
			}
		}
		change := contractChange{From: &old.Revision, To: current.Revision, Diff: contractLineDiff(old.Text, current.Text)}
		if jsonOut {
			return printJSON(a.stdout, change)
		}
		_, err = a.stdout.Write([]byte(change.Diff))
		return err
	}
	report := contractStatusReport{Manifest: name, Active: state.Active, Local: current, LastSuccessfulRemoteCheck: state.LastSuccessfulRemoteCheck, Roots: []contractRootStatus{}, LiveDelivery: "unsupported: no registered launch; relaunch through my ai or manually reload"}
	for _, instance := range contractstate.SortedInstances(state) {
		report.Instances = append(report.Instances, contractInstanceStatus{Instance: instance, Status: contractstate.InstanceStatus(instance, state.Active)})
	}
	if len(report.Instances) != 0 {
		report.LiveDelivery = "per-launch emission receipts; model acceptance and behavior are not verified"
	}
	for _, r := range sortedContractRoots(state) {
		status := "current"
		if state.Pending(r.Path) || state.Active != nil && current.Revision != state.Active.Revision {
			status = "pending"
		}
		if current.Provenance == "unpublished" {
			status = "pending-unpublished"
		}
		report.Roots = append(report.Roots, contractRootStatus{Root: r, Status: status})
	}
	if jsonOut {
		return printJSON(a.stdout, report)
	}
	fmt.Fprintf(a.stdout, "manifest\t%s\nlocal\t%s\t%s\t%s\n", name, current.Provenance, current.Revision.ManifestCommit, current.Revision.Blob)
	if state.Active == nil && !current.Configured {
		fmt.Fprintln(a.stdout, "active\tnot configured")
	} else if state.Active == nil {
		fmt.Fprintln(a.stdout, "active\tunobserved; run my sync to initialize revision tracking")
	} else {
		fmt.Fprintf(a.stdout, "active\t%s\t%s\n", state.Active.Revision.ManifestCommit, state.Active.Revision.Blob)
	}
	checked := state.LastSuccessfulRemoteCheck
	if checked == "" {
		checked = "unknown (no successful remote check recorded)"
	}
	fmt.Fprintf(a.stdout, "last-successful-remote-check\t%s\n", checked)
	for _, r := range report.Roots {
		fmt.Fprintf(a.stdout, "root\t%s\t%s\t%s\n", r.Status, r.Path, r.LastError)
	}
	for _, instance := range report.Instances {
		fmt.Fprintf(a.stdout, "instance\t%s\t%s\t%s\t%s\n", instance.ID, instance.Harness, instance.Status, instance.Message)
	}
	fmt.Fprintf(a.stdout, "live-delivery\t%s\n", report.LiveDelivery)
	return nil
}

func contractSnapshotAt(ref manifest.Ref, rev string) (contractstate.Snapshot, error) {
	commit, err := gitPRText(ref.LocalPath, nil, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return contractstate.Snapshot{}, fmt.Errorf("cannot resolve local contract revision %q", rev)
	}
	raw, err := gitPRBytes(ref.LocalPath, nil, "show", commit+":manifest.json")
	if err != nil {
		return contractstate.Snapshot{}, fmt.Errorf("manifest.json is unavailable at %s", commit)
	}
	var doc manifest.Document
	if err = json.Unmarshal(raw, &doc); err != nil {
		return contractstate.Snapshot{}, err
	}
	var c manifest.Contract
	if doc.ContractFile != "" {
		if doc.Contract != nil || !manifest.ValidContractFilePath(doc.ContractFile) {
			return contractstate.Snapshot{}, fmt.Errorf("invalid contract source at %s", commit)
		}
		data, err := gitPRBytes(ref.LocalPath, nil, "show", commit+":"+doc.ContractFile)
		if err != nil {
			return contractstate.Snapshot{}, fmt.Errorf("contract file is unavailable at %s", commit)
		}
		c, err = manifest.ParseContractFile(doc.ContractFile, data)
		if err != nil {
			return contractstate.Snapshot{}, err
		}
	} else {
		c, err = manifest.LoadContract("", doc)
		if err != nil {
			return contractstate.Snapshot{}, err
		}
	}
	text := string(c.Bytes())
	if c.File == "" && text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return contractstate.Snapshot{Configured: c.File != "" || len(c.Rules()) != 0, Source: c.Source(), Path: c.File, Text: text, Provenance: "committed", Revision: contractstate.Revision{ManifestCommit: commit, Blob: manifest.ContractBlob([]byte(text))}}, nil
}
