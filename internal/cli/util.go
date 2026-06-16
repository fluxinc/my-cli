package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

type stringListFlag []string

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func printHumanField(w io.Writer, label, value string) {
	const width = 88
	text := strings.Join(strings.Fields(value), " ")
	if text == "" {
		return
	}
	firstPrefix := "  " + label + ": "
	nextPrefix := strings.Repeat(" ", len(firstPrefix))
	line := firstPrefix
	for _, word := range strings.Fields(text) {
		if line == firstPrefix {
			line += word
			continue
		}
		if len(line)+1+len(word) <= width {
			line += " " + word
			continue
		}
		fmt.Fprintln(w, line)
		line = nextPrefix + word
	}
	fmt.Fprintln(w, line)
}

func expandUserPath(home, path string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return os.UserHomeDir()
}

func stringInSlice(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func pruneStrings(values []string, remove map[string]bool) []string {
	out := values[:0]
	for _, value := range values {
		if !remove[value] {
			out = append(out, value)
		}
	}
	return out
}

func samePath(a, b string) bool {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absA); err == nil {
		absA = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absB); err == nil {
		absB = resolved
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func pathWithinRoot(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func commandLine(command string, args []string) string {
	parts := append([]string{command}, args...)
	return strings.Join(parts, " ")
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type commandErrorPayload struct {
	Error       string `json:"error"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type structuredCommandError struct {
	code        string
	message     string
	remediation string
}

func noUmbrellaError(message, remediation string) error {
	return structuredCommandError{
		code:        "no_umbrella",
		message:     message,
		remediation: remediation,
	}
}

func (a app) maybeJSONError(jsonOut bool, err error) error {
	if !jsonOut {
		return err
	}
	payload := commandErrorPayload{
		Error:   "command_failed",
		Message: err.Error(),
	}
	var structured structuredCommandError
	if errors.As(err, &structured) {
		payload.Error = structured.code
		payload.Message = structured.message
		payload.Remediation = structured.remediation
	} else if strings.Contains(err.Error(), "no my umbrella found") {
		payload.Error = "no_umbrella"
		payload.Remediation = "run my setup or pass --umbrella <path>"
	}
	if printErr := printJSON(a.stdout, payload); printErr != nil {
		return printErr
	}
	return errAlreadyPrinted
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func parseInterspersed(fs *flag.FlagSet, args []string, valueFlags map[string]bool) ([]string, error) {
	var flagArgs []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if isFlagArg(arg) {
			flagArgs = append(flagArgs, arg)
			name := flagName(arg)
			if valueFlags[name] && !strings.Contains(arg, "=") {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("missing value for %s", arg)
				}
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positional, nil
}

func isFlagArg(arg string) bool {
	return strings.HasPrefix(arg, "-") && arg != "-"
}

func flagName(arg string) string {
	arg = strings.TrimLeft(arg, "-")
	if i := strings.Index(arg, "="); i >= 0 {
		return arg[:i]
	}
	return arg
}

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	cleaned := strings.TrimSpace(value)
	if cleaned != "" {
		*f = append(*f, cleaned)
	}
	return nil
}

func (e structuredCommandError) Error() string {
	return e.message
}
