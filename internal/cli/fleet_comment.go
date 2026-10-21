package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

func (a app) runFleetComment(args []string) error {
	var opts meetingCommonOpts
	var bodyFile, key string
	fs := newFlagSet("my fleet comment", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	fs.StringVar(&bodyFile, "body-file", "", "UTF-8 comment file, or - for stdin")
	fs.StringVar(&key, "idempotency-key", "", "unique submission key; reuse when retrying")
	flags := meetingValueFlags()
	flags["body-file"] = true
	flags["idempotency-key"] = true
	rest, err := parseInterspersed(fs, args, flags)
	if err != nil {
		return err
	}
	if len(rest) != 1 || bodyFile == "" || !regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`).MatchString(key) {
		return fmt.Errorf("usage: my fleet comment <id|identifier> --body-file FILE --idempotency-key KEY (16-128 letters, digits, - or _; reuse the same key when retrying)")
	}
	var data []byte
	if bodyFile == "-" {
		if a.stdin == nil {
			return fmt.Errorf("--body-file - needs stdin")
		}
		data, err = io.ReadAll(io.LimitReader(a.stdin, 1<<20))
	} else {
		data, err = os.ReadFile(bodyFile)
	}
	if err != nil {
		return err
	}
	body := strings.TrimSpace(string(data))
	if len([]rune(body)) == 0 || len([]rune(body)) > 2000 {
		return fmt.Errorf("comment must contain 1-2000 characters")
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return err
	}
	svc, err := boundFleetService(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return err
	}
	if svc == nil {
		return fmt.Errorf("conversation comments require a service-backed fleet")
	}
	match, err := resolveOneFleet(roots, svc, rest[0])
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, describeServiceError(svc, err))
	}
	if match.local != nil {
		return fmt.Errorf("%s is a markdown record, not a service conversation", match.local.ID)
	}
	client, err := svc.connect()
	if err != nil {
		return err
	}
	ctx, cancel := serviceContext()
	defer cancel()
	note, created, err := client.Comment(ctx, match.remote.ID(), key, body, match.remote.Revisions())
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, describeServiceError(svc, err))
	}
	if opts.jsonOut {
		return printJSON(a.stdout, map[string]any{"id": match.remote.ID(), "note": note, "created": created, "idempotency_key": key})
	}
	action := "already posted"
	if created {
		action = "posted"
	}
	fmt.Fprintf(a.stdout, "%s: comment %s in %s (submission %s)\n", match.remote.ID(), action, svc.service.ID, key)
	return nil
}
