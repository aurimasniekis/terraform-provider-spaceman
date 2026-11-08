//go:build !linux

package provider

import "os/exec"

// setGatewayProcAttr does nothing where Pdeathsig doesn't exist: t.Cleanup stops the demo gateway.
func setGatewayProcAttr(*exec.Cmd) {}
