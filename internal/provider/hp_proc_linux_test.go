//go:build linux

package provider

import (
	"os/exec"
	"syscall"
)

// setGatewayProcAttr has the kernel send the demo gateway SIGTERM when the test binary dies (a `go test -timeout` panic
// or a SIGKILL, where t.Cleanup never runs). Pdeathsig follows the thread that started the child; the Go runtime does
// not end threads of goroutines that aren't locked to one, so in practice that is the life of the test binary.
func setGatewayProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
