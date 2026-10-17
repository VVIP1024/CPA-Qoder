//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() }
}
