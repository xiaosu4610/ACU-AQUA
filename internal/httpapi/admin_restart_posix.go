//go:build !windows

// POSIX 平台：systemctl 自重启需脱离当前进程会话（Setsid），
// 否则重启命令会随网关进程一起被 systemd 终止。
package httpapi

import (
	"os/exec"
	"syscall"
)

func detachStart(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
