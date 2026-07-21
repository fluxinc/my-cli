package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	governancecheck "github.com/fluxinc/my-cli/internal/governance"
)

func (a app) runGovernance(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing governance subcommand")
	}
	switch args[0] {
	case "check":
		return a.runGovernanceCheck(args[1:])
	case "audit":
		return a.runGovernanceAudit(args[1:])
	case "-h", "--help", "help":
		a.printGovernanceUsage()
		return nil
	default:
		return fmt.Errorf("unknown governance subcommand %q", args[0])
	}
}

func (a app) runGovernanceCheck(args []string) error {
	var input governancecheck.CheckInput
	var jsonOut bool
	fs := newFlagSet("my governance check", a.stderr)
	fs.StringVar(&input.Repo, "repo", ".", "local protected repository checkout")
	fs.StringVar(&input.Repository, "repository", "", "protected GitHub owner/repository")
	fs.StringVar(&input.BaseRef, "base", "", "trusted pull request base commit/ref")
	fs.StringVar(&input.HeadRef, "head", "", "proposed pull request head commit/ref")
	fs.StringVar(&input.ManifestRepo, "manifest-repo", "", "local manifest Git checkout")
	fs.StringVar(&input.ManifestBaseRef, "manifest-base", "", "trusted manifest base commit/ref")
	fs.StringVar(&input.ManifestPath, "manifest-path", "manifest.json", "manifest path within its repository")
	fs.StringVar(&input.AttestationRepo, "attestation-repo", "", "local authoritative attestation repository checkout")
	fs.StringVar(&input.AttestationRepository, "attestation-repository", "", "authoritative attestation GitHub owner/repository")
	fs.StringVar(&input.AttestationBaseRef, "attestation-base", "", "trusted attestation default-branch commit/ref")
	fs.StringVar(&input.Mount, "mount", "", "protected mount id or @manifest")
	fs.Int64Var(&input.ActorID, "actor-id", 0, "immutable pull request author GitHub id")
	fs.StringVar(&input.ActorLogin, "actor-login", "", "pull request author GitHub login")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		a.printGovernanceUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"repo": true, "repository": true, "base": true, "head": true, "manifest-repo": true,
		"manifest-base": true, "manifest-path": true, "attestation-repo": true,
		"attestation-repository": true, "attestation-base": true, "mount": true,
		"actor-id": true, "actor-login": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("governance check does not accept positional arguments")
	}
	if input.Runner == nil {
		if a.publishRunner != nil {
			input.Runner = governancecheck.Runner(a.publishRunner)
		} else {
			input.Runner = governanceExec
		}
	}
	report, err := governancecheck.Check(input)
	if err != nil {
		return err
	}
	if jsonOut {
		if err := printJSON(a.stdout, report); err != nil {
			return err
		}
	} else {
		status := "denied"
		if report.Allowed {
			status = "allowed"
		}
		fmt.Fprintf(a.stdout, "governance\t%s\t%s\t%s\tactor=%d\tpermission=%s\n", status, report.Repository, report.Mount, report.ActorID, report.ActorPermission)
		fmt.Fprintf(a.stdout, "trusted-base\t%s\t%s\n", report.ManifestCommit, report.ManifestBaseRef)
		for _, violation := range report.Violations {
			fmt.Fprintf(a.stdout, "violation\t%s\t%s\t%s\t%s\n", violation.ReasonCode, violation.Mode, violation.Path, violation.Commit)
		}
	}
	if !report.Allowed {
		codes := make([]string, 0, len(report.Violations))
		for _, violation := range report.Violations {
			if !stringSliceContains(codes, violation.ReasonCode) {
				codes = append(codes, violation.ReasonCode)
			}
		}
		return fmt.Errorf("governance check denied change (%s); next: revise the pull request without protected-history violations or obtain repository-admin approval where the trusted base policy permits it", strings.Join(codes, ","))
	}
	return nil
}

func (a app) printGovernanceUsage() {
	fmt.Fprintln(a.stderr, `Usage:
  my governance check --repo DIR --repository OWNER/REPO --base SHA --head SHA \
    --manifest-repo DIR --manifest-base SHA --mount ID|@manifest \
    [--attestation-repo DIR --attestation-repository OWNER/REPO --attestation-base SHA] \
    --actor-id GITHUB_NUMERIC_ID --actor-login LOGIN [--manifest-path PATH] [--json]
  my governance audit [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]

The manifest and all governance rules are read from --manifest-base, never from
the proposed head. In pull-request CI, pass github.event.pull_request.user.id
and .login; commit authors, committers, GITHUB_ACTOR, and local inventory are
not accepted substitutes for the immutable pull-request author identity.`)
}

func governanceExec(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1")
	return cmd.CombinedOutput()
}
