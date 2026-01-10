package gpu

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type DRMDevice struct {
	Card       string // e.g. card2
	VendorHex  string // e.g. 0x1002
	BusyPct    *int   // nil if not available
	BusySource string // gpu_busy_percent / gt_busy_percent
}

func readIntFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return v, nil
}

// QueryDRMDevices reads best-effort GPU utilization from Linux DRM sysfs.
// On many systems:
// - AMD exposes /sys/class/drm/cardX/device/gpu_busy_percent
// - Intel may expose /sys/class/drm/cardX/device/gt_busy_percent
func QueryDRMDevices() ([]DRMDevice, error) {
	cards, err := filepath.Glob("/sys/class/drm/card*")
	if err != nil {
		return nil, err
	}
	var out []DRMDevice
	for _, cardPath := range cards {
		card := filepath.Base(cardPath)
		vendorPath := filepath.Join(cardPath, "device", "vendor")
		vendorB, err := os.ReadFile(vendorPath)
		if err != nil {
			continue
		}
		vendorHex := strings.TrimSpace(string(vendorB))

		var busy *int
		busySource := ""
		if v, err := readIntFile(filepath.Join(cardPath, "device", "gpu_busy_percent")); err == nil {
			busy = &v
			busySource = "gpu_busy_percent"
		} else if v, err := readIntFile(filepath.Join(cardPath, "device", "gt_busy_percent")); err == nil {
			busy = &v
			busySource = "gt_busy_percent"
		}

		out = append(out, DRMDevice{
			Card:       card,
			VendorHex:  vendorHex,
			BusyPct:    busy,
			BusySource: busySource,
		})
	}
	return out, nil
}
