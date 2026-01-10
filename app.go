package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/gpu"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/nvidia"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/power"
)

type App struct {
	ctx context.Context

	// Best-effort device labels from lspci (optional)
	lspciIGPU string
	lspciDGPU string
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go a.detectGPUs()
}

func (a *App) detectGPUs() {
	devs, err := gpu.Detect()
	if err != nil {
		return
	}
	i, d := gpu.Summarize(devs)
	if len(i) > 0 {
		a.lspciIGPU = i[0]
	}
	if len(d) > 0 {
		a.lspciDGPU = d[0]
	}
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
}

type ActionResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

type PowerLimits struct {
	Enforced float64 `json:"enforced"`
	Default  float64 `json:"default"`
	Max      float64 `json:"max"`
	Error    string  `json:"error,omitempty"`
}

func (a *App) GetOverview() Overview {
	ov := Overview{Timestamp: time.Now().Unix()}

	ov.PowerdStatus = power.PowerdStatus()
	proc, _ := nvidia.QueryComputeProcesses()
	ov.Processes = proc

	drmDevs, _ := gpu.QueryDRMDevices()
	ov.IGPUText = formatIGPU(a.lspciIGPU, drmDevs)

	info, err := nvidia.QueryGpu()
	if err != nil {
		ov.NvidiaAvailable = false
		if a.lspciDGPU != "" {
			ov.NvidiaName = shortenLspci(a.lspciDGPU)
		} else {
			ov.NvidiaName = "nvidia-smi unavailable"
		}
		ov.Error = err.Error()
		return ov
	}

	ov.NvidiaAvailable = true
	ov.NvidiaName = info.Name
	ov.Util = info.Util
	ov.Temp = info.Temp
	ov.MemUsed = info.MemUsed
	ov.MemTotal = info.MemTotal
	ov.PwrDraw = info.PwrDraw
	ov.PwrEnforced = info.PwrEnforced
	ov.PwrDefault = info.PwrDefault
	ov.PwrMax = info.PwrMax
	return ov
}

func (a *App) GetPowerLimits() PowerLimits {
	enf, def, max, _, err := power.GetLimits()
	if err != nil {
		return PowerLimits{Error: err.Error()}
	}
	return PowerLimits{Enforced: enf, Default: def, Max: max}
}

func (a *App) EnablePowerd() ActionResult {
	out, err := power.EnablePowerd()
	if err != nil {
		return ActionResult{OK: false, Output: out, Error: err.Error()}
	}
	return ActionResult{OK: true, Output: out}
}

func (a *App) EnableMaxPower() ActionResult {
	out, err := power.SetPowerLimitMax()
	if err != nil {
		return ActionResult{OK: false, Output: out, Error: err.Error()}
	}
	return ActionResult{OK: true, Output: out}
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

func formatIGPU(lspciLine string, drmDevs []gpu.DRMDevice) string {
	name := shortenLspci(lspciLine)

	for _, d := range drmDevs {
		vendor := gpu.VendorName(d.VendorHex)
		if vendor == "NVIDIA" {
			continue
		}
		util := "N/A"
		if d.BusyPct != nil {
			util = fmt.Sprintf("%d%%", *d.BusyPct)
		}

		if name != "" {
			return fmt.Sprintf("iGPU: %s (%s/%s: %s)", name, vendor, d.Card, util)
		}
		return fmt.Sprintf("iGPU: %s(%s): %s", vendor, d.Card, util)
	}

	if name != "" {
		return "iGPU: " + name
	}
	return "iGPU: (none)"
}
