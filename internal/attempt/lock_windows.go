//go:build windows

package attempt

import "os"

// Attempt capture has no verified lock on native Windows. my ai still
// launches there; it records nothing and my attempts list says so.
const lockSupported = false

func tryLock(*os.File) (bool, error) { return false, ErrUnsupported }

func unlock(*os.File) {}

// ProcessStart cannot establish process identity on native Windows.
func ProcessStart(int) (string, bool, error) { return "", false, ErrUnsupported }

// ProbeChild always answers unknown on native Windows.
func ProbeChild(ProcessRef) string { return ChildUnknown }
