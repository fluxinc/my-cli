package recordindex

import (
	"os"
	"testing"

	"github.com/fluxinc/my-cli/internal/testenv"
)

// realHome is the user's home before testenv isolates the process; only the
// opt-in real-qmd smoke test reads it, to check the user's own qmd files.
var realHome, _ = os.UserHomeDir()

func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}
