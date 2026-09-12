//go:build windows

// Windows 开发环境：无 systemd，重启动作留空（仅本地调试用）。
package httpapi

import "os/exec"

func detachStart(cmd *exec.Cmd) error {
	return cmd.Start()
}
