package main

import (
	"os/exec"
	"runtime"
)

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default: // linux and other unix-likes
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
