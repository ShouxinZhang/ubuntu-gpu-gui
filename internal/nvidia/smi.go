package nvidia

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

type GpuInfo struct {
	Name        string
	Temp        string
	PwrDraw     string
	PwrEnforced string
	PwrDefault  string
	PwrMax      string
	MemUsed     string
	MemTotal    string
	Util        string
}

func HasNvidiaSMI() bool {
	_, err := exec.LookPath("nvidia-smi")
	return err == nil
}

func QueryGpu() (*GpuInfo, error) {
	cmdArgs := []string{
		"--query-gpu=name,temperature.gpu,power.draw,enforced.power.limit,power.default_limit,power.max_limit,memory.used,memory.total,utilization.gpu",
		"--format=csv,noheader,nounits",
	}
	cmd := exec.Command("nvidia-smi", cmdArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	line := strings.TrimSpace(out.String())
	parts := strings.Split(line, ",")
	if len(parts) < 9 {
		return nil, fmt.Errorf("unexpected nvidia-smi output: %s", line)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	return &GpuInfo{
		Name:        parts[0],
		Temp:        parts[1],
		PwrDraw:     parts[2],
		PwrEnforced: parts[3],
		PwrDefault:  parts[4],
		PwrMax:      parts[5],
		MemUsed:     parts[6],
		MemTotal:    parts[7],
		Util:        parts[8],
	}, nil
}

func QueryComputeProcesses() (string, error) {
	cmdArgs := []string{
		"--query-compute-apps=pid,process_name,used_memory",
		"--format=csv,noheader",
	}
	cmd := exec.Command("nvidia-smi", cmdArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// If no processes are running, nvidia-smi might return non-0 or empty
		return "No processes running", nil
	}

	output := strings.TrimSpace(out.String())
	if output == "" {
		return "No processes running", nil
	}
	return output, nil
}
