package gpu

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
)

type Kind string

const (
	KindIntegrated Kind = "iGPU"
	KindDiscrete   Kind = "dGPU"
)

type Device struct {
	Kind Kind
	Name string
}

// Detect returns a best-effort list of GPU devices from lspci.
// This does not provide utilization metrics; it's for labeling iGPU vs dGPU.
func Detect() ([]Device, error) {
	if _, err := exec.LookPath("lspci"); err != nil {
		return nil, err
	}

	cmd := exec.Command("lspci")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var rawLines []string
	hasNvidia := false
	scanner := bufio.NewScanner(bytes.NewReader(out.Bytes()))
	for scanner.Scan() {
		line := scanner.Text()
		lower := strings.ToLower(line)
		if strings.Contains(lower, "vga compatible controller") || strings.Contains(lower, "3d controller") {
			rawLines = append(rawLines, line)
			if strings.Contains(lower, "nvidia") {
				hasNvidia = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var devices []Device
	for _, line := range rawLines {
		name := strings.TrimSpace(line)
		lower := strings.ToLower(line)

		// Heuristic:
		// - If NVIDIA exists, assume NVIDIA is dGPU and the other GPU is iGPU (common hybrid laptops)
		// - Otherwise, Intel is iGPU; others default to dGPU
		kind := KindDiscrete
		if strings.Contains(lower, "nvidia") {
			kind = KindDiscrete
		} else if hasNvidia {
			kind = KindIntegrated
		} else if strings.Contains(lower, "intel") {
			kind = KindIntegrated
		}
		devices = append(devices, Device{Kind: kind, Name: name})
	}
	return devices, nil
}

func Summarize(devices []Device) (integrated []string, discrete []string) {
	for _, d := range devices {
		if d.Kind == KindIntegrated {
			integrated = append(integrated, d.Name)
		} else {
			discrete = append(discrete, d.Name)
		}
	}
	return integrated, discrete
}
