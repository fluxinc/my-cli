package worksession

import (
	"os"
	"testing"

	"github.com/fluxinc/my-cli/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}
