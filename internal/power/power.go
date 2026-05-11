package power

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/nvidia"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/system"
)

const (
	nvidiaPowerdService = "nvidia-powerd.service"
	defaultUnitPath     = "/etc/systemd/system/nvidia-powerd.service"
	defaultPowerdPath   = "/usr/bin/nvidia-powerd"

	statusTimeout = 2 * time.Second
	actionTimeout = 45 * time.Second
)

type commandRunner interface {
	RunWithOptions(ctx context.Context, options system.RunOptions, name string, args ...string) (string, error)
}

type statFunc func(string) (os.FileInfo, error)
type createTempFunc func(string, string) (*os.File, error)

// Limits contains the power limit data for the GPU selected by the service.
type Limits struct {
	GPU      nvidia.GPUIdentity
	Enforced float64
	Default  float64
	Max      float64
}

// Service owns power-related orchestration and privileged command execution.
type Service struct {
	runner    commandRunner
	queryGPUs func(context.Context) ([]nvidia.GPU, error)
	hasSMI    func() bool
	stat      statFunc
	createTmp createTempFunc
	remove    func(string) error

	unitPath   string
	powerdPath string

	actionMu sync.Mutex
}

// NewService returns a power service backed by the host environment.
func NewService() *Service {
	return &Service{
		runner:     system.NewRunner(),
		queryGPUs:  nvidia.QueryGPUs,
		hasSMI:     nvidia.HasNvidiaSMI,
		stat:       os.Stat,
		createTmp:  os.CreateTemp,
		remove:     os.Remove,
		unitPath:   defaultUnitPath,
		powerdPath: defaultPowerdPath,
	}
}

func (s *Service) PowerdStatus(ctx context.Context) string {
	ctx = contextOrBackground(ctx)
	out, err := s.run(ctx, system.RunOptions{Timeout: statusTimeout}, "systemctl", "is-active", nvidiaPowerdService)
	status := strings.TrimSpace(out)
	if status != "" {
		return status
	}
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "systemctl N/A"
		}
		return err.Error()
	}
	return "--"
}

func (s *Service) EnablePowerd(ctx context.Context) (string, error) {
	ctx = contextOrBackground(ctx)
	s.actionMu.Lock()
	defer s.actionMu.Unlock()

	if !s.hasSMI() {
		return "", fmt.Errorf("nvidia-smi not found")
	}
	if _, err := s.stat(s.powerdPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s not found", s.powerdPath)
		}
		return "", err
	}

	var outputs []string
	out, err := s.ensurePowerdUnit(ctx)
	outputs = appendOutput(outputs, out)
	if err != nil {
		return strings.Join(outputs, "\n"), err
	}

	out, err = s.runRoot(ctx, "systemctl", "enable", "--now", nvidiaPowerdService)
	outputs = appendOutput(outputs, out)
	if err != nil {
		return strings.Join(outputs, "\n"), err
	}
	return strings.Join(outputs, "\n"), nil
}

func (s *Service) ensurePowerdUnit(ctx context.Context) (string, error) {
	if _, err := s.stat(s.unitPath); err == nil {
		return "", nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmp, err := s.createTmp("", "nvidia-powerd-*.service")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer s.remove(tmpName)

	if _, err := tmp.WriteString(powerdUnit(s.powerdPath)); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	var outputs []string
	out, err := s.runRoot(ctx, "install", "-m", "0644", tmpName, s.unitPath)
	outputs = appendOutput(outputs, out)
	if err != nil {
		return strings.Join(outputs, "\n"), err
	}
	out, err = s.runRoot(ctx, "systemctl", "daemon-reload")
	outputs = appendOutput(outputs, out)
	if err != nil {
		return strings.Join(outputs, "\n"), err
	}
	return strings.Join(outputs, "\n"), nil
}

func (s *Service) SetPowerLimitWatts(ctx context.Context, gpuIndex int, limit float64) (string, error) {
	ctx = contextOrBackground(ctx)
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	return s.setPowerLimitWatts(ctx, gpuIndex, limit)
}

func (s *Service) setPowerLimitWatts(ctx context.Context, gpuIndex int, limit float64) (string, error) {
	limitInt, err := normalizePowerLimit(limit)
	if err != nil {
		return "", err
	}
	if gpuIndex < 0 {
		return "", fmt.Errorf("invalid GPU index %d", gpuIndex)
	}
	return s.runRoot(ctx, "nvidia-smi", "-i", strconv.Itoa(gpuIndex), "-pl", strconv.FormatInt(limitInt, 10))
}

func (s *Service) GetLimits(ctx context.Context) (Limits, error) {
	ctx = contextOrBackground(ctx)
	gpus, err := s.queryGPUs(ctx)
	if err != nil {
		return Limits{}, err
	}
	if len(gpus) == 0 {
		return Limits{}, fmt.Errorf("no NVIDIA GPUs found")
	}
	return limitsFromGPU(gpus[0])
}

func (s *Service) SetPowerLimitMax(ctx context.Context) (string, error) {
	ctx = contextOrBackground(ctx)
	s.actionMu.Lock()
	defer s.actionMu.Unlock()

	limits, err := s.GetLimits(ctx)
	if err != nil {
		return "", err
	}
	return s.setPowerLimitWatts(ctx, limits.GPU.Index, limits.Max)
}

func limitsFromGPU(gpu nvidia.GPU) (Limits, error) {
	enforced := gpu.Metrics.PowerLimitEnforcedW
	def := gpu.Metrics.PowerLimitDefaultW
	max := gpu.Metrics.PowerLimitMaxW
	if enforced == nil || def == nil || max == nil {
		return Limits{}, fmt.Errorf("power limits unavailable for GPU %d (%s)", gpu.Index, gpu.Name)
	}
	return Limits{
		GPU:      gpu.GPUIdentity,
		Enforced: *enforced,
		Default:  *def,
		Max:      *max,
	}, nil
}

func normalizePowerLimit(limit float64) (int64, error) {
	if math.IsNaN(limit) || math.IsInf(limit, 0) {
		return 0, fmt.Errorf("invalid power limit %.2f W", limit)
	}
	limitInt := int64(math.Round(limit))
	if limitInt <= 0 {
		return 0, fmt.Errorf("invalid power limit %.2f W", limit)
	}
	return limitInt, nil
}

func powerdUnit(powerdPath string) string {
	return fmt.Sprintf(`[Unit]
Description=NVIDIA powerd (Dynamic Boost)
After=multi-user.target
ConditionPathExists=%s

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
`, powerdPath, powerdPath)
}

func (s *Service) runRoot(ctx context.Context, name string, args ...string) (string, error) {
	return s.run(ctx, system.RunOptions{RequireRoot: true, Timeout: actionTimeout}, name, args...)
}

func (s *Service) run(ctx context.Context, options system.RunOptions, name string, args ...string) (string, error) {
	if s.runner == nil {
		s.runner = system.NewRunner()
	}
	return s.runner.RunWithOptions(ctx, options, name, args...)
}

func appendOutput(outputs []string, out string) []string {
	out = strings.TrimSpace(out)
	if out == "" {
		return outputs
	}
	return append(outputs, out)
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
