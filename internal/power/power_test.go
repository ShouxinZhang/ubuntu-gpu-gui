package power

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/nvidia"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/system"
)

type runnerCall struct {
	options system.RunOptions
	name    string
	args    []string
}

type fakeRunner struct {
	calls []runnerCall
	out   string
	err   error
}

func (r *fakeRunner) RunWithOptions(ctx context.Context, options system.RunOptions, name string, args ...string) (string, error) {
	r.calls = append(r.calls, runnerCall{
		options: options,
		name:    name,
		args:    append([]string(nil), args...),
	})
	return r.out, r.err
}

func TestEnablePowerdInstallsMissingUnitAsPrivilegedTransaction(t *testing.T) {
	runner := &fakeRunner{out: "ok"}
	svc := testService(runner)
	svc.stat = func(path string) (os.FileInfo, error) {
		switch path {
		case svc.powerdPath:
			return fakeFileInfo{}, nil
		case svc.unitPath:
			return nil, os.ErrNotExist
		default:
			t.Fatalf("unexpected stat path %q", path)
			return nil, os.ErrNotExist
		}
	}

	out, err := svc.EnablePowerd(context.Background())
	if err != nil {
		t.Fatalf("EnablePowerd returned error: %v", err)
	}
	if out != "ok\nok\nok" {
		t.Fatalf("unexpected output: %q", out)
	}
	assertCall(t, runner.calls, 0, true, "install", []string{"-m", "0644"})
	assertCall(t, runner.calls, 1, true, "systemctl", []string{"daemon-reload"})
	assertCall(t, runner.calls, 2, true, "systemctl", []string{"enable", "--now", nvidiaPowerdService})
	if got := runner.calls[0].args[len(runner.calls[0].args)-1]; got != svc.unitPath {
		t.Fatalf("install target = %q, want %q", got, svc.unitPath)
	}
}

func TestEnablePowerdSkipsExistingUnitInstall(t *testing.T) {
	runner := &fakeRunner{out: "started"}
	svc := testService(runner)
	svc.stat = func(path string) (os.FileInfo, error) {
		return fakeFileInfo{}, nil
	}

	if _, err := svc.EnablePowerd(context.Background()); err != nil {
		t.Fatalf("EnablePowerd returned error: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one command, got %d", len(runner.calls))
	}
	assertCall(t, runner.calls, 0, true, "systemctl", []string{"enable", "--now", nvidiaPowerdService})
}

func TestSetPowerLimitWattsUsesArgvAndRoot(t *testing.T) {
	runner := &fakeRunner{out: "ok"}
	svc := testService(runner)

	output, err := svc.SetPowerLimitWatts(context.Background(), 1, 249.6)
	if err != nil {
		t.Fatalf("SetPowerLimitWatts returned error: %v", err)
	}
	if output != "ok" {
		t.Fatalf("expected command output, got %q", output)
	}
	assertCall(t, runner.calls, 0, true, "nvidia-smi", []string{"-i", "1", "-pl", "250"})
}

func TestSetPowerLimitMaxTargetsQueriedGPU(t *testing.T) {
	runner := &fakeRunner{out: "ok"}
	svc := testService(runner)
	svc.queryGPUs = func(ctx context.Context) ([]nvidia.GPU, error) {
		return []nvidia.GPU{testGPU(2, 250, 320, 450)}, nil
	}

	if _, err := svc.SetPowerLimitMax(context.Background()); err != nil {
		t.Fatalf("SetPowerLimitMax returned error: %v", err)
	}
	assertCall(t, runner.calls, 0, true, "nvidia-smi", []string{"-i", "2", "-pl", "450"})
}

func TestSetPowerLimitWattsRejectsInvalidInput(t *testing.T) {
	runner := &fakeRunner{out: "ok"}
	svc := testService(runner)

	tests := []struct {
		name  string
		index int
		limit float64
	}{
		{name: "negative index", index: -1, limit: 250},
		{name: "nan limit", index: 0, limit: math.NaN()},
		{name: "zero limit", index: 0, limit: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.SetPowerLimitWatts(context.Background(), tt.index, tt.limit)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner should not be called, got %d calls", len(runner.calls))
	}
}

func TestGetLimitsRejectsUnavailablePowerMetrics(t *testing.T) {
	svc := testService(&fakeRunner{})
	svc.queryGPUs = func(ctx context.Context) ([]nvidia.GPU, error) {
		gpu := testGPU(0, 250, 320, 450)
		gpu.Metrics.PowerLimitMaxW = nil
		return []nvidia.GPU{gpu}, nil
	}

	_, err := svc.GetLimits(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "power limits unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPowerdStatusUsesTimeoutAndReturnsOutputOnNonzeroExit(t *testing.T) {
	runner := &fakeRunner{out: "inactive\n", err: errors.New("exit status 3")}
	svc := testService(runner)

	got := svc.PowerdStatus(context.Background())
	if got != "inactive" {
		t.Fatalf("PowerdStatus() = %q, want inactive", got)
	}
	assertCall(t, runner.calls, 0, false, "systemctl", []string{"is-active", nvidiaPowerdService})
	if runner.calls[0].options.Timeout != statusTimeout {
		t.Fatalf("status timeout = %v, want %v", runner.calls[0].options.Timeout, statusTimeout)
	}
}

func testService(runner *fakeRunner) *Service {
	tmpFiles := make([]string, 0)
	return &Service{
		runner: runner,
		queryGPUs: func(ctx context.Context) ([]nvidia.GPU, error) {
			return []nvidia.GPU{testGPU(0, 250, 320, 450)}, nil
		},
		hasSMI: func() bool { return true },
		stat: func(path string) (os.FileInfo, error) {
			return fakeFileInfo{}, nil
		},
		createTmp: func(dir, pattern string) (*os.File, error) {
			f, err := os.CreateTemp(dir, pattern)
			if err == nil {
				tmpFiles = append(tmpFiles, f.Name())
			}
			return f, err
		},
		remove: func(path string) error {
			for i, tmp := range tmpFiles {
				if tmp == path {
					tmpFiles = append(tmpFiles[:i], tmpFiles[i+1:]...)
					break
				}
			}
			return os.Remove(path)
		},
		unitPath:   "/etc/systemd/system/nvidia-powerd.service",
		powerdPath: "/usr/bin/nvidia-powerd",
	}
}

func testGPU(index int, enforced, def, max float64) nvidia.GPU {
	return nvidia.GPU{
		GPUIdentity: nvidia.GPUIdentity{
			Index:    index,
			UUID:     "GPU-test",
			PCIBusID: "00000000:01:00.0",
			Name:     "NVIDIA Test GPU",
		},
		Metrics: nvidia.GPUMetrics{
			PowerLimitEnforcedW: &enforced,
			PowerLimitDefaultW:  &def,
			PowerLimitMaxW:      &max,
		},
	}
}

func assertCall(t *testing.T, calls []runnerCall, index int, requireRoot bool, name string, argsPrefix []string) {
	t.Helper()
	if len(calls) <= index {
		t.Fatalf("missing call %d; calls=%+v", index, calls)
	}
	call := calls[index]
	if call.options.RequireRoot != requireRoot {
		t.Fatalf("call %d RequireRoot = %v, want %v", index, call.options.RequireRoot, requireRoot)
	}
	if call.name != name {
		t.Fatalf("call %d name = %q, want %q", index, call.name, name)
	}
	if len(call.args) < len(argsPrefix) {
		t.Fatalf("call %d args too short: got %v want prefix %v", index, call.args, argsPrefix)
	}
	for i, want := range argsPrefix {
		if call.args[i] != want {
			t.Fatalf("call %d arg %d = %q, want %q; args=%v", index, i, call.args[i], want, call.args)
		}
	}
}

type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "fake" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0644 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }
