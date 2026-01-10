package system

import (
	"fmt"
	"os"
	"os/exec"
)

func IsRoot() bool {
	return os.Geteuid() == 0
}

// RunShell runs a shell command. If requireRoot is true and we are not root,
// it tries to elevate using pkexec first (GUI-friendly), then sudo.
func RunShell(command string, requireRoot bool) (string, error) {
	var cmd *exec.Cmd
	if requireRoot && !IsRoot() {
		if _, err := exec.LookPath("pkexec"); err == nil {
			cmd = exec.Command("pkexec", "sh", "-c", command)
		} else if _, err := exec.LookPath("sudo"); err == nil {
			// -n: non-interactive; safer for GUI (fails fast if password is needed)
			cmd = exec.Command("sudo", "-n", "sh", "-c", command)
		} else {
			return "", fmt.Errorf("root privileges required but pkexec/sudo not found")
		}
	} else {
		cmd = exec.Command("sh", "-c", command)
	}

	out, err := cmd.CombinedOutput()
	return string(out), err
}
