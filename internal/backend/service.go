package backend

import (
	"context"
	"sync"
	"time"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/gpu"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/nvidia"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/power"
)

const defaultOverviewTTL = 750 * time.Millisecond

type DeviceSnapshot struct {
	Devices   []gpu.Device
	Ready     bool
	Error     string
	UpdatedAt int64
}

type Overview struct {
	Timestamp    int64
	PowerdStatus string
	GPUs         []nvidia.GPU
	Processes    []nvidia.ComputeProcess
	DRMDevices   []gpu.DRMDevice
	Devices      DeviceSnapshot
	Warnings     []string
}

type Service struct {
	detectGPUs     func(context.Context) ([]gpu.Device, error)
	queryDRM       func() ([]gpu.DRMDevice, error)
	queryGPUs      func(context.Context) ([]nvidia.GPU, error)
	queryProcesses func(context.Context) ([]nvidia.ComputeProcess, error)
	powerStatus    func(context.Context) string
	powerLimits    func(context.Context) (power.Limits, error)
	enablePowerd   func(context.Context) (string, error)
	setMaxPower    func(context.Context) (string, error)
	now            func() time.Time

	deviceMu sync.RWMutex
	devices  DeviceSnapshot

	overviewMu    sync.Mutex
	overviewCache Overview
	overviewUntil time.Time
	overviewTTL   time.Duration
}

func NewService() *Service {
	powerService := power.NewService()
	return &Service{
		detectGPUs:     gpu.DetectContext,
		queryDRM:       gpu.QueryDRMDevices,
		queryGPUs:      nvidia.QueryGPUs,
		queryProcesses: nvidia.QueryComputeProcessesContext,
		powerStatus:    powerService.PowerdStatus,
		powerLimits:    powerService.GetLimits,
		enablePowerd:   powerService.EnablePowerd,
		setMaxPower:    powerService.SetPowerLimitMax,
		now:            time.Now,
		overviewTTL:    defaultOverviewTTL,
	}
}

func (s *Service) Start(ctx context.Context) {
	go s.RefreshDevices(ctx)
}

func (s *Service) RefreshDevices(ctx context.Context) DeviceSnapshot {
	devices, err := s.detectGPUs(ctx)
	snap := DeviceSnapshot{
		Devices:   devices,
		Ready:     true,
		UpdatedAt: s.now().Unix(),
	}
	if err != nil {
		snap.Error = err.Error()
	}

	s.deviceMu.Lock()
	s.devices = snap
	s.deviceMu.Unlock()
	return snap
}

func (s *Service) DeviceSnapshot() DeviceSnapshot {
	s.deviceMu.RLock()
	defer s.deviceMu.RUnlock()
	return cloneDeviceSnapshot(s.devices)
}

func (s *Service) Overview(ctx context.Context) Overview {
	s.overviewMu.Lock()
	defer s.overviewMu.Unlock()

	now := s.now()
	if !s.overviewUntil.IsZero() && now.Before(s.overviewUntil) {
		return cloneOverview(s.overviewCache)
	}

	ov := Overview{
		Timestamp:    now.Unix(),
		PowerdStatus: s.powerStatus(ctx),
	}

	devices := s.DeviceSnapshot()
	if !devices.Ready {
		devices = s.RefreshDevices(ctx)
	}
	if devices.Error != "" {
		ov.Warnings = append(ov.Warnings, "GPU detection: "+devices.Error)
	}
	ov.Devices = devices

	drm, err := s.queryDRM()
	if err != nil {
		ov.Warnings = append(ov.Warnings, "DRM: "+err.Error())
	} else {
		ov.DRMDevices = drm
	}

	gpus, err := s.queryGPUs(ctx)
	if err != nil {
		ov.Warnings = append(ov.Warnings, "NVIDIA GPU: "+err.Error())
	} else {
		ov.GPUs = gpus
	}

	processes, err := s.queryProcesses(ctx)
	if err != nil {
		ov.Warnings = append(ov.Warnings, "NVIDIA processes: "+err.Error())
	} else {
		ov.Processes = processes
	}

	s.overviewCache = cloneOverview(ov)
	s.overviewUntil = now.Add(s.overviewTTL)
	return ov
}

func (s *Service) PowerLimits(ctx context.Context) (power.Limits, error) {
	return s.powerLimits(ctx)
}

func (s *Service) EnablePowerd(ctx context.Context) (string, error) {
	return s.enablePowerd(ctx)
}

func (s *Service) SetMaxPower(ctx context.Context) (string, error) {
	return s.setMaxPower(ctx)
}

func cloneDeviceSnapshot(in DeviceSnapshot) DeviceSnapshot {
	out := in
	out.Devices = append([]gpu.Device(nil), in.Devices...)
	return out
}

func cloneOverview(in Overview) Overview {
	out := in
	out.GPUs = append([]nvidia.GPU(nil), in.GPUs...)
	out.Processes = append([]nvidia.ComputeProcess(nil), in.Processes...)
	out.DRMDevices = append([]gpu.DRMDevice(nil), in.DRMDevices...)
	out.Warnings = append([]string(nil), in.Warnings...)
	out.Devices = cloneDeviceSnapshot(in.Devices)
	return out
}
