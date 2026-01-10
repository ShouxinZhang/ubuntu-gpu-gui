package power

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/nvidia"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/system"
)

func PowerdStatus() string {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "systemctl N/A"
	}
	cmd := exec.Command("systemctl", "is-active", "nvidia-powerd.service")
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out))
}

func ensurePowerdServiceIfRoot() error {
	if !system.IsRoot() {
		return nil
	}
	unitPath := "/etc/systemd/system/nvidia-powerd.service"
	if _, err := os.Stat(unitPath); err == nil {
		return nil
	}
	if _, err := os.Stat("/usr/bin/nvidia-powerd"); os.IsNotExist(err) {
		return nil
	}

	unit := `[Unit]
Description=NVIDIA powerd (Dynamic Boost)
After=multi-user.target
ConditionPathExists=/usr/bin/nvidia-powerd

[Service]
Type=simple
ExecStart=/usr/bin/nvidia-powerd
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
`

	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return err
	}
	exec.Command("systemctl", "daemon-reload").Run()
	return nil
}

func EnablePowerd() (string, error) {
	if !nvidia.HasNvidiaSMI() {
		return "", fmt.Errorf("nvidia-smi not found")
	}
	if _, err := os.Stat("/usr/bin/nvidia-powerd"); os.IsNotExist(err) {
		return "", fmt.Errorf("/usr/bin/nvidia-powerd not found")
	}

	_ = ensurePowerdServiceIfRoot()

	if _, err := exec.LookPath("systemctl"); err == nil {
		out, err := system.RunShell("systemctl enable --now nvidia-powerd.service", true)
		return out, err
	}
	// Fallback: start directly
	out, err := system.RunShell("/usr/bin/nvidia-powerd", true)
	return out, err
}

func SetPowerLimitWatts(limit float64) (string, error) {
	// nvidia-smi -pl expects a value in W; we pass an integer for safety.
	limitInt := int64(limit + 0.5)
	out, err := system.RunShell(fmt.Sprintf("nvidia-smi -pl %d", limitInt), true)
	return out, err
}

func GetLimits() (enforced float64, def float64, max float64, raw *nvidia.GpuInfo, err error) {
	info, err := nvidia.QueryGpu()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	parse := func(s string) (float64, error) {
		return strconv.ParseFloat(strings.TrimSpace(s), 64)
	}
	enforced, err1 := parse(info.PwrEnforced)
	def, err2 := parse(info.PwrDefault)
	max, err3 := parse(info.PwrMax)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, info, fmt.Errorf("failed to parse power limits: enforced=%q default=%q max=%q", info.PwrEnforced, info.PwrDefault, info.PwrMax)
	}
	return enforced, def, max, info, nil
}

func SetPowerLimitMax() (string, error) {
	_, _, max, _, err := GetLimits()
	if err != nil {
		return "", err
	}
	return SetPowerLimitWatts(max)
}
