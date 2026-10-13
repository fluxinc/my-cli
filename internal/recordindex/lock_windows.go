//go:build windows

package recordindex

import (
	"context"
	"errors"
)

// Native Windows is not a supported execution target for my (use WSL).
func lockDir(context.Context, string) (func(), error) {
	return nil, errors.New("the semantic index is not supported on native Windows; use WSL")
}

func privateUmask() func() { return func() {} }
