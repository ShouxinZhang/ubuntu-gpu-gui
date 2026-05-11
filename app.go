package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/backend"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/gpu"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/nvidia"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/power"
)

const (
	overviewTimeout = 4 * time.Second
	actionTimeout   = 60 * time.Second
)

type App struct {
	ctx     context.Context
	service *backend.Service

	actionMu sync.Mutex
}

func NewApp() *App {
	return &App{service: backend.NewService()}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.service.Start(ctx)
}

type Overview struct {
	Timestamp int64 `json:"timestamp"`

	NvidiaAvailable bool   `json:"nvidiaAvailable"`
	NvidiaName      string `json:"nvidiaName"`
	Util            string `json:"util"`
	Temp            string `json:"temp"`
	MemUsed         string `json:"memUsed"`
	MemTotal        string `json:"memTotal"`
	PwrDraw         string `json:"pwrDraw"`
	PwrEnforced     string `json:"pwrEnforced"`
	PwrDefault      string `json:"pwrDefault"`
	PwrMax          string `json:"pwrMax"`

	PowerdStatus string `json:"powerdStatus"`
	Processes    string `json:"processes"`

	IGPUText string `json:"iGpuText"`
	Error    string `json:"error,omitempty"`

	GPUs         []GPUOverview     `json:"gpus"`
	ProcessList  []ProcessOverview `json:"processList"`
	Warnings     []string          `json:"warnings,omitempty"`
	DevicesReady bool              `json:"devicesReady"`
	DeviceError  string            `json:"deviceError,omitempty"`
}

type GPUOverview struct {
	Index                 int      `json:"index"`
	UUID                  string   `json:"uuid"`
	PCIBusID              string   `json:"pciBusId"`
	Name                  string   `json:"name"`
	TemperatureC          *float64 `json:"temperatureC,omitempty"`
	PowerDrawW            *float64 `json:"powerDrawW,omitempty"`
	PowerLimitEnforcedW   *float64 `json:"powerLimitEnforcedW,omitempty"`
	PowerLimitDefaultW    *float64 `json:"powerLimitDefaultW,omitempty"`
	PowerLimitMaxW        *float64 `json:"powerLimitMaxW,omitempty"`
	MemoryUsedMiB         *int64   `json:"memoryUsedMiB,omitempty"`
	MemoryTotalMiB        *int64   `json:"memoryTotalMiB,omitempty"`
	UtilizationGPUPercent *float64 `json:"utilizationGpuPercent,omitempty"`
}

type ProcessOverview struct {
	GPUUUID       string `json:"gpuUuid"`
	GPUPCIBusID   string `json:"gpuPciBusId"`
	PID           int    `json:"pid"`
	ProcessName   string `json:"processName"`
	UsedMemoryMiB *int64 `json:"usedMemoryMiB,omitempty"`
}

type ActionResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

type PowerLimits struct {
	GPUIndex int    `json:"gpuIndex"`
	GPUUUID  string `json:"gpuUuid"`
	PCIBusID string `json:"pciBusId"`
	Name     string `json:"name"`

	Enforced float64 `json:"enforced"`
	Default  float64 `json:"default"`
	Max      float64 `json:"max"`
	Error    string  `json:"error,omitempty"`
}

func (a *App) GetOverview() Overview {
	ctx, cancel := a.requestContext(overviewTimeout)
	defer cancel()

	snap := a.service.Overview(ctx)
	ov := overviewDTO(snap)
	if len(ov.GPUs) == 0 {
		ov.NvidiaAvailable = false
		ov.NvidiaName = fallbackDGPUName(snap.Devices.Devices)
		if ov.NvidiaName == "" {
			ov.NvidiaName = "nvidia-smi unavailable"
		}
		ov.Error = firstNvidiaWarning(snap.Warnings)
		return ov
	}

	first := snap.GPUs[0]
	ov.NvidiaAvailable = true
	ov.NvidiaName = first.Name
	ov.Util = formatFloatPtr(first.Metrics.UtilizationGPUPercent, 0)
	ov.Temp = formatFloatPtr(first.Metrics.TemperatureC, 0)
	ov.MemUsed = formatIntPtr(first.Metrics.MemoryUsedMiB)
	ov.MemTotal = formatIntPtr(first.Metrics.MemoryTotalMiB)
	ov.PwrDraw = formatFloatPtr(first.Metrics.PowerDrawW, 2)
	ov.PwrEnforced = formatFloatPtr(first.Metrics.PowerLimitEnforcedW, 2)
	ov.PwrDefault = formatFloatPtr(first.Metrics.PowerLimitDefaultW, 2)
	ov.PwrMax = formatFloatPtr(first.Metrics.PowerLimitMaxW, 2)
	return ov
}

func (a *App) GetPowerLimits() PowerLimits {
	ctx, cancel := a.requestContext(overviewTimeout)
	defer cancel()

	limits, err := a.service.PowerLimits(ctx)
	if err != nil {
		return PowerLimits{Error: err.Error()}
	}
	return powerLimitsDTO(limits)
}

func (a *App) EnablePowerd() ActionResult {
	return a.runAction(func(ctx context.Context) (string, error) {
		return a.service.EnablePowerd(ctx)
	})
}

func (a *App) EnableMaxPower() ActionResult {
	return a.runAction(func(ctx context.Context) (string, error) {
		return a.service.SetMaxPower(ctx)
	})
}

func (a *App) requestContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, timeout)
}

func (a *App) runAction(fn func(context.Context) (string, error)) ActionResult {
	a.actionMu.Lock()
	defer a.actionMu.Unlock()

	ctx, cancel := a.requestContext(actionTimeout)
	defer cancel()

	out, err := fn(ctx)
	if err != nil {
		return ActionResult{OK: false, Output: out, Error: err.Error()}
	}
	return ActionResult{OK: true, Output: out}
}

func overviewDTO(snap backend.Overview) Overview {
	return Overview{
		Timestamp:    snap.Timestamp,
		PowerdStatus: snap.PowerdStatus,
		Processes:    formatProcessList(snap.Processes),
		IGPUText:     formatIGPU(snap.Devices.Devices, snap.DRMDevices),
		GPUs:         gpuDTOs(snap.GPUs),
		ProcessList:  processDTOs(snap.Processes),
		Warnings:     append([]string(nil), snap.Warnings...),
		DevicesReady: snap.Devices.Ready,
		DeviceError:  snap.Devices.Error,
	}
}

func gpuDTOs(gpus []nvidia.GPU) []GPUOverview {
	out := make([]GPUOverview, 0, len(gpus))
	for _, g := range gpus {
		out = append(out, GPUOverview{
			Index:                 g.Index,
			UUID:                  g.UUID,
			PCIBusID:              g.PCIBusID,
			Name:                  g.Name,
			TemperatureC:          g.Metrics.TemperatureC,
			PowerDrawW:            g.Metrics.PowerDrawW,
			PowerLimitEnforcedW:   g.Metrics.PowerLimitEnforcedW,
			PowerLimitDefaultW:    g.Metrics.PowerLimitDefaultW,
			PowerLimitMaxW:        g.Metrics.PowerLimitMaxW,
			MemoryUsedMiB:         g.Metrics.MemoryUsedMiB,
			MemoryTotalMiB:        g.Metrics.MemoryTotalMiB,
			UtilizationGPUPercent: g.Metrics.UtilizationGPUPercent,
		})
	}
	return out
}

func processDTOs(processes []nvidia.ComputeProcess) []ProcessOverview {
	out := make([]ProcessOverview, 0, len(processes))
	for _, p := range processes {
		out = append(out, ProcessOverview{
			GPUUUID:       p.GPUUUID,
			GPUPCIBusID:   p.GPUPCIBusID,
			PID:           p.PID,
			ProcessName:   p.ProcessName,
			UsedMemoryMiB: p.UsedMemoryMiB,
		})
	}
	return out
}

func powerLimitsDTO(l power.Limits) PowerLimits {
	return PowerLimits{
		GPUIndex: l.GPU.Index,
		GPUUUID:  l.GPU.UUID,
		PCIBusID: l.GPU.PCIBusID,
		Name:     l.GPU.Name,
		Enforced: l.Enforced,
		Default:  l.Default,
		Max:      l.Max,
	}
}

func formatProcessList(processes []nvidia.ComputeProcess) string {
	if len(processes) == 0 {
		return "No processes running"
	}
	lines := make([]string, 0, len(processes))
	for _, p := range processes {
		mem := "N/A"
		if p.UsedMemoryMiB != nil {
			mem = strconv.FormatInt(*p.UsedMemoryMiB, 10) + " MiB"
		}
		gpuID := p.GPUPCIBusID
		if gpuID == "" {
			gpuID = p.GPUUUID
		}
		if gpuID == "" {
			lines = append(lines, fmt.Sprintf("%d, %s, %s", p.PID, p.ProcessName, mem))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %d, %s, %s", gpuID, p.PID, p.ProcessName, mem))
		}
	}
	return strings.Join(lines, "\n")
}

func formatIGPU(devices []gpu.Device, drmDevs []gpu.DRMDevice) string {
	for _, d := range devices {
		if d.Kind != gpu.KindIntegrated {
			continue
		}
		util := drmUtilForDevice(d, drmDevs)
		name := shortDeviceName(d)
		if name != "" {
			return fmt.Sprintf("iGPU: %s: %s", name, util)
		}
		return "iGPU: " + util
	}

	for _, d := range drmDevs {
		vendor := gpu.VendorName(d.VendorHex)
		if vendor == "NVIDIA" {
			continue
		}
		util := "N/A"
		if d.BusyPct != nil {
			util = fmt.Sprintf("%d%%", *d.BusyPct)
		}
		if vendor != "" {
			return fmt.Sprintf("iGPU: %s(%s): %s", vendor, d.Card, util)
		}
		return fmt.Sprintf("iGPU: %s: %s", d.Card, util)
	}

	return "iGPU: (none)"
}

func drmUtilForDevice(device gpu.Device, drmDevs []gpu.DRMDevice) string {
	for _, d := range drmDevs {
		if device.BusID != "" && d.BusID != "" && strings.EqualFold(device.BusID, d.BusID) {
			if d.BusyPct != nil {
				return fmt.Sprintf("%d%%", *d.BusyPct)
			}
			return "N/A"
		}
	}
	return "N/A"
}

func shortDeviceName(device gpu.Device) string {
	if device.BusID != "" && device.DeviceName != "" {
		return device.BusID + " " + device.DeviceName
	}
	if device.Name != "" {
		return shortenLspci(device.Name)
	}
	return device.DeviceName
}

func fallbackDGPUName(devices []gpu.Device) string {
	for _, d := range devices {
		if d.Kind == gpu.KindDiscrete {
			return shortDeviceName(d)
		}
	}
	return ""
}

func firstNvidiaWarning(warnings []string) string {
	for _, warning := range warnings {
		if strings.Contains(warning, "NVIDIA") {
			return warning
		}
	}
	if len(warnings) > 0 {
		return warnings[0]
	}
	return ""
}

func shortenLspci(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	bus := strings.SplitN(line, " ", 2)[0]
	parts := strings.Split(line, ": ")
	tail := line
	if len(parts) >= 2 {
		tail = parts[len(parts)-1]
	}
	if bus != "" && tail != "" {
		return bus + " " + tail
	}
	return line
}

func formatFloatPtr(value *float64, precision int) string {
	if value == nil {
		return "N/A"
	}
	return strconv.FormatFloat(*value, 'f', precision, 64)
}

func formatIntPtr(value *int64) string {
	if value == nil {
		return "N/A"
	}
	return strconv.FormatInt(*value, 10)
}
