//go:build !darwin && !linux

package credcache

import (
	"net"
	"os/exec"
)

// Supported is false until this platform's IPC is verified per-user; commands
// run in direct mode and say so.
const Supported = false

func ensurePrivateDir(string) error            { return ErrUnsupported }
func verifyPrivateDir(string) error            { return ErrUnsupported }
func tryLockFile(string) (func(), bool, error) { return nil, false, ErrUnsupported }
func hardenBrokerProcess()                     {}
func Detach(*exec.Cmd)                         {}
func isNotRunning(error) bool                  { return true }
func peerIsCurrentUser(net.Conn) bool          { return false }
