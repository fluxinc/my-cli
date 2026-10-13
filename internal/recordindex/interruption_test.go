package recordindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/recordsvc"
)

type interruptedService struct {
	*fakeService
	failID string
}

func (s *interruptedService) Get(ctx context.Context, id string) (recordsvc.Record, error) {
	if id == s.failID {
		return nil, errors.New("temporary service failure")
	}
	return s.fakeService.Get(ctx, id)
}

func TestInterruptedPageThenRevertedRecordRepairsDocument(t *testing.T) {
	svc := &interruptedService{fakeService: newFakeService()}
	svc.put("a", "original alpha")
	svc.put("b", "original bravo")
	ix := openTest(t, t.TempDir(), svc, &fakeQMD{}, "https://registry.example.test/api")
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	svc.put("a", "temporary alpha")
	svc.put("b", "changed bravo")
	svc.failID = "b"
	if _, err := ix.Sync(context.Background(), 0, false); err == nil {
		t.Fatal("interrupted page succeeded")
	}
	// The first record returns to its original value before the page retries.
	svc.put("a", "original alpha")
	svc.failID = ""
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	st, err := ix.load()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(ix.genDir(st.Generation), "docs", st.Docs["a"].File))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "original alpha") || strings.Contains(string(data), "temporary alpha") {
		t.Fatalf("recovered document still contains an interrupted value: %s", data)
	}
}
