package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fluxinc/my-cli/internal/meetings"
)

func (a app) runMeetings(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing meetings subcommand")
	}
	switch args[0] {
	case "list":
		return a.runMeetingsList(args[1:])
	case "search":
		return a.runMeetingsSearch(args[1:])
	case "get":
		return a.runMeetingsGet(args[1:])
	case "add":
		return a.runMeetingsAdd(args[1:])
	case "-h", "--help", "help":
		a.printMeetingsUsage()
		return nil
	default:
		return fmt.Errorf("unknown meetings subcommand %q", args[0])
	}
}

func (a app) printMeetingsUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my meetings list [--manifest NAME] [--workspace ID] [--since DATE] [--customer ID] [--partner ID] [--product ID] [--json]
  my meetings search <text> [--manifest NAME] [--workspace ID] [--customer ID] [--partner ID] [--product ID] [--json]
  my meetings get <id|path> [--manifest NAME] [--workspace ID] [--json]
  my meetings add <slug> [--manifest NAME] [--workspace ID] [--date DATE] [--title TEXT] [--customer ID] [--attendees NAME] [--partner ID] [--product ID] [--source-id ID] [--print] [--json]

Meeting commands use local markdown files under workspace meetings/ directories.`)
}

func (a app) runMeetingsList(args []string) error {
	opts, rest, err := parseMeetingReadOpts("my meetings list", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("meetings list does not accept positional arguments")
	}
	roots, err := meetingRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter := a.resolveMeetingFilter(opts.home, opts.manifestName, opts.umbrellaRoot, opts.filter())
	found, err := meetings.List(roots, filter)
	if err != nil {
		return err
	}
	return a.printMeetings(found, opts.jsonOut)
}

func (a app) runMeetingsSearch(args []string) error {
	opts, rest, err := parseMeetingReadOpts("my meetings search", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my meetings search <text>")
	}
	roots, err := meetingRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter := a.resolveMeetingFilter(opts.home, opts.manifestName, opts.umbrellaRoot, opts.filter())
	found, err := meetingSearch(roots, rest[0], filter)
	if err != nil {
		return err
	}
	return a.printMeetings(found, opts.jsonOut)
}

func (a app) runMeetingsGet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my meetings get", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my meetings get <id|path>")
	}
	roots, err := meetingRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	meeting, content, err := meetings.Get(roots, rest[0])
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Meeting meetings.Meeting `json:"meeting"`
			Content string           `json:"content"`
		}{Meeting: meeting, Content: content})
	}
	fmt.Fprint(a.stdout, content)
	return nil
}

func (a app) runMeetingsAdd(args []string) error {
	var opts meetingAddOpts
	fs := newFlagSet("my meetings add", a.stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.date, "date", "", "meeting date, YYYY-MM-DD")
	fs.StringVar(&opts.title, "title", "", "meeting title")
	fs.StringVar(&opts.customer, "customer", "", "customer ID")
	fs.Var(&opts.attendees, "attendees", "meeting attendee (repeatable)")
	fs.Var(&opts.partners, "partner", "partner ID (repeatable)")
	fs.StringVar(&opts.product, "product", "", "product ID")
	fs.StringVar(&opts.sourceID, "source-id", "", "source system identifier")
	fs.StringVar(&opts.status, "status", "", "meeting status")
	fs.BoolVar(&opts.printOnly, "print", false, "print the scaffold without writing a file")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":      true,
		"manifest":  true,
		"workspace": true,
		"umbrella":  true,
		"date":      true,
		"title":     true,
		"customer":  true,
		"attendees": true,
		"partner":   true,
		"product":   true,
		"source-id": true,
		"status":    true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my meetings add <slug>")
	}
	roots, err := meetingRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(roots) != 1 {
		return fmt.Errorf("meetings add requires exactly one workspace; pass --manifest and --workspace")
	}
	customer := a.resolveCustomerForWrite(opts.home, opts.manifestName, opts.umbrellaRoot, opts.customer)
	meeting, content, err := meetings.Add(roots[0], rest[0], meetings.AddOptions{
		Date:      opts.date,
		Title:     opts.title,
		Customer:  customer,
		Attendees: []string(opts.attendees),
		Partners:  []string(opts.partners),
		Product:   opts.product,
		SourceID:  opts.sourceID,
		Status:    opts.status,
		DryRun:    opts.printOnly,
	})
	if err != nil {
		return err
	}
	if !opts.printOnly {
		if err := markRecordIntentToAdd(roots[0], meeting.Path); err != nil {
			return err
		}
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Meeting meetings.Meeting `json:"meeting"`
			Content string           `json:"content,omitempty"`
		}{Meeting: meeting, Content: content})
	}
	if opts.printOnly {
		fmt.Fprintf(a.stdout, "# path: %s\n%s", meeting.Path, content)
		return nil
	}
	fmt.Fprintln(a.stdout, meeting.Path)
	return nil
}

type meetingCommonOpts struct {
	home         string
	manifestName string
	workspaceID  string
	umbrellaRoot string
	jsonOut      bool
}

type meetingReadOpts struct {
	meetingCommonOpts
	since    string
	customer string
	partner  string
	product  string
}

type meetingAddOpts struct {
	meetingCommonOpts
	date      string
	title     string
	customer  string
	attendees stringListFlag
	partners  stringListFlag
	product   string
	sourceID  string
	status    string
	printOnly bool
}

func parseMeetingReadOpts(name string, stderr io.Writer, args []string) (meetingReadOpts, []string, error) {
	var opts meetingReadOpts
	fs := newFlagSet(name, stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.since, "since", "", "minimum meeting date, YYYY-MM-DD")
	fs.StringVar(&opts.customer, "customer", "", "customer ID")
	fs.StringVar(&opts.partner, "partner", "", "partner ID")
	fs.StringVar(&opts.product, "product", "", "product ID")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":      true,
		"manifest":  true,
		"workspace": true,
		"umbrella":  true,
		"since":     true,
		"customer":  true,
		"partner":   true,
		"product":   true,
	})
	return opts, rest, err
}

func bindMeetingCommonFlags(fs *flag.FlagSet, opts *meetingCommonOpts) {
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&opts.workspaceID, "workspace", "", "limit to one workspace ID")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON")
}

func meetingValueFlags() map[string]bool {
	return map[string]bool{
		"home":      true,
		"manifest":  true,
		"workspace": true,
		"umbrella":  true,
	}
}

func (opts meetingReadOpts) filter() meetings.Filter {
	return meetings.Filter{
		Since:    opts.since,
		Customer: opts.customer,
		Partner:  opts.partner,
		Product:  opts.product,
	}
}

func meetingRoots(home, manifestName, workspaceID, umbrellaRoot string) ([]meetings.Root, error) {
	return contentRoots(home, manifestName, workspaceID, umbrellaRoot, "meeting", []string{"handbook", "meetings"})
}

func (a app) printMeetings(found []meetings.Meeting, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, found)
	}
	for _, meeting := range found {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			meeting.Date,
			meeting.ID,
			meeting.Title,
			meeting.Customer,
			strings.Join(meeting.Partners, ","),
			meeting.Product,
			meeting.Snippet,
			meeting.Path,
		)
	}
	return nil
}

var meetingSearch = defaultMeetingSearch

var qmdMeetingSearch = runQMDMeetingSearch

func defaultMeetingSearch(roots []meetings.Root, query string, filter meetings.Filter) ([]meetings.Meeting, error) {
	qmdFound, qmdOK := qmdMeetingSearch(roots, query, filter)
	fallback, err := meetings.Search(roots, query, filter)
	if err != nil {
		return nil, err
	}
	if qmdOK && len(qmdFound) != 0 {
		return mergeMeetingResults(qmdFound, fallback), nil
	}
	return fallback, nil
}

func runQMDMeetingSearch(roots []meetings.Root, query string, filter meetings.Filter) ([]meetings.Meeting, bool) {
	found, err := meetings.List(roots, filter)
	if err != nil {
		return nil, false
	}
	return runQMDContentSearch(found, query, "meetings",
		func(meeting meetings.Meeting) string { return meeting.Path },
		func(meeting meetings.Meeting, snippet string) meetings.Meeting {
			meeting.Snippet = snippet
			return meeting
		})
}

func mergeMeetingResults(primary, fallback []meetings.Meeting) []meetings.Meeting {
	return mergeContentResults(primary, fallback, func(meeting meetings.Meeting) string { return meeting.Path })
}

func (a app) resolveMeetingFilter(home, manifestName, umbrellaRoot string, filter meetings.Filter) meetings.Filter {
	if filter.Customer == "" {
		return filter
	}
	customer, ok, err := a.findCustomer(home, manifestName, umbrellaRoot, filter.Customer)
	if err != nil || !ok {
		return filter
	}
	filter.CustomerValues = uniqueStrings(append(append([]string{filter.Customer, customer.ID, customer.Domain}, customer.Aliases...), customer.Name))
	return filter
}
