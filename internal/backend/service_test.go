package backend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/gpu"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/nvidia"
)

func TestOverviewCachesWithinTTL(t *testing.T) {
	now := time.Unix(100, 0)
	queryCount := 0
	svc := testBackendService(&now)
	svc.queryGPUs = func(ctx context.Context) ([]nvidia.GPU, error) {
		queryCount++
		return []nvidia.GPU{testBackendGPU()}, nil
	}

	first := svc.Overview(context.Background())
	second := svc.Overview(context.Background())

	if queryCount != 1 {
		t.Fatalf("expected cached second overview, queryCount=%d", queryCount)
	}
	if first.Timestamp != second.Timestamp {
		t.Fatalf("cached timestamp changed: first=%d second=%d", first.Timestamp, second.Timestamp)
	}
}

func TestOverviewCollectsPartialWarnings(t *testing.T) {
	now := time.Unix(100, 0)
	svc := testBackendService(&now)
	svc.detectGPUs = func(ctx context.Context) ([]gpu.Device, error) {
		return nil, errors.New("lspci missing")
	}
	svc.queryDRM = func() ([]gpu.DRMDevice, error) {
		return nil, errors.New("sysfs denied")
	}
	svc.queryGPUs = func(ctx context.Context) ([]nvidia.GPU, error) {
		return nil, errors.New("driver unavailable")
	}
	svc.queryProcesses = func(ctx context.Context) ([]nvidia.ComputeProcess, error) {
		return nil, errors.New("process query failed")
	}

	ov := svc.Overview(context.Background())
	if len(ov.Warnings) != 4 {
		t.Fatalf("expected 4 warnings, got %d: %v", len(ov.Warnings), ov.Warnings)
	}
	if !ov.Devices.Ready || ov.Devices.Error == "" {
		t.Fatalf("expected ready device snapshot with error, got %+v", ov.Devices)
	}
}

func testBackendService(now *time.Time) *Service {
	svc := &Service{
		detectGPUs: func(ctx context.Context) ([]gpu.Device, error) {
			return []gpu.Device{{Kind: gpu.KindIntegrated, BusID: "0000:00:02.0"}}, nil
		},
		queryDRM: func() ([]gpu.DRMDevice, error) {
			return nil, nil
		},
		queryGPUs: func(ctx context.Context) ([]nvidia.GPU, error) {
			return []nvidia.GPU{testBackendGPU()}, nil
		},
		queryProcesses: func(ctx context.Context) ([]nvidia.ComputeProcess, error) {
			return nil, nil
		},
		powerStatus: func(ctx context.Context) string {
			return "active"
		},
		now: func() time.Time {
			return *now
		},
		overviewTTL: time.Second,
	}
	return svc
}

func testBackendGPU() nvidia.GPU {
	util := 42.0
	return nvidia.GPU{
		GPUIdentity: nvidia.GPUIdentity{
			Index:    0,
			UUID:     "GPU-test",
			PCIBusID: "00000000:01:00.0",
			Name:     "NVIDIA Test GPU",
		},
		Metrics: nvidia.GPUMetrics{
			UtilizationGPUPercent: &util,
		},
	}
}
