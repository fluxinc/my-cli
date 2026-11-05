//go:build !darwin && !linux

package credcache

import (
	"errors"
	"testing"
)

func TestUnsupportedPlatformRefusesWithoutFetching(t *testing.T) {
	calls := 0
	_, err := (&Client{}).Get(Request{Key: Key{Service: "s"}, Resolve: func() (string, error) {
		calls++
		return "fake", nil
	}}, Settings{})
	if !errors.Is(err, ErrUnsupported) || calls != 0 {
		t.Fatalf("%v calls=%d", err, calls)
	}
	if err = (&Client{}).Lock(); err != nil {
		t.Fatal(err)
	}
}
