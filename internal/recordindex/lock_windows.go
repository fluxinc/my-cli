//go:build windows

package recordindex

import "errors"

// Native Windows is not a supported execution target for my (use WSL).
func lockDir(string) (func(), error) {
	return nil, errors.New("the semantic index is not supported on native Windows; use WSL")
}

func privateUmask() func() { return func() {} }
