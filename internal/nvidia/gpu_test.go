package nvidia

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseGPUsParsesMultiGPUOutput(t *testing.T) {
	output := strings.Join([]string{
		"0, GPU-111, 00000000:01:00.0, NVIDIA GeForce RTX 3080, 55, 120.50, 250.00, 320.00, 370.00, 1024, 24576, 42",
		"1, GPU-222, 00000000:02:00.0, NVIDIA GeForce RTX 4090, 44, 80.00, 300.00, 350.00, 450.00, 2048, 24576, 15",
	}, "\n")

	gpus, err := parseGPUs(output)
	if err != nil {
		t.Fatalf("parseGPUs returned error: %v", err)
	}
	if len(gpus) != 2 {
		t.Fatalf("expected 2 GPU rows, got %d", len(gpus))
	}

	first := gpus[0]
	if first.Index != 0 || first.UUID != "GPU-111" || first.PCIBusID != "00000000:01:00.0" {
		t.Fatalf("unexpected first GPU identity: %+v", first.GPUIdentity)
	}
	if first.Name != "NVIDIA GeForce RTX 3080" {
		t.Fatalf("expected first GPU name, got %q", first.Name)
	}
	assertFloat64Ptr(t, "first utilization", first.Metrics.UtilizationGPUPercent, 42)

	second := gpus[1]
	if second.Index != 1 || second.Name != "NVIDIA GeForce RTX 4090" {
		t.Fatalf("unexpected second GPU identity: %+v", second.GPUIdentity)
	}
	assertInt64Ptr(t, "second memory used", second.Metrics.MemoryUsedMiB, 2048)
}

func TestParseGPUsKeepsNAMetricsNullable(t *testing.T) {
	output := "0, GPU-111, 00000000:01:00.0, NVIDIA GeForce RTX 3080, N/A, N/A, N/A, 320.00, 370.00, N/A, 24576, N/A"

	gpus, err := parseGPUs(output)
	if err != nil {
		t.Fatalf("parseGPUs returned error: %v", err)
	}

	metrics := gpus[0].Metrics
	if metrics.TemperatureC != nil {
		t.Fatalf("expected nil temperature, got %v", *metrics.TemperatureC)
	}
	if metrics.PowerDrawW != nil || metrics.PowerLimitEnforcedW != nil {
		t.Fatalf("expected nil N/A power metrics, got %+v", metrics)
	}
	if metrics.MemoryUsedMiB != nil || metrics.UtilizationGPUPercent != nil {
		t.Fatalf("expected nil N/A memory/utilization metrics, got %+v", metrics)
	}
	assertFloat64Ptr(t, "default power limit", metrics.PowerLimitDefaultW, 320)
	assertInt64Ptr(t, "memory total", metrics.MemoryTotalMiB, 24576)
}

func TestQueryGPUsParsesOnlyStdoutOnSuccess(t *testing.T) {
	client := Client{run: func(ctx context.Context, args ...string) (smiOutput, error) {
		return smiOutput{
			Stdout: "0, GPU-111, 00000000:01:00.0, NVIDIA GeForce RTX 3080, 55, 120.50, 250.00, 320.00, 370.00, 1024, 24576, 42",
			Stderr: "warning: this line is not csv and must not be parsed",
		}, nil
	}}

	gpus, err := client.QueryGPUs(context.Background())
	if err != nil {
		t.Fatalf("QueryGPUs returned error: %v", err)
	}
	if len(gpus) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(gpus))
	}
	if gpus[0].UUID != "GPU-111" {
		t.Fatalf("unexpected GPU: %+v", gpus[0])
	}
}

func TestQueryGPUsPropagatesNvidiaSMIFailureWithStderr(t *testing.T) {
	client := Client{run: func(ctx context.Context, args ...string) (smiOutput, error) {
		return smiOutput{
			Stderr: "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver.",
		}, errors.New("exit status 1")
	}}

	_, err := client.QueryGPUs(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "query gpu") {
		t.Fatalf("expected operation context in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "couldn't communicate") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestQueryComputeProcessesContextParsesProcessesAndEnrichesBusID(t *testing.T) {
	client := Client{run: func(ctx context.Context, args ...string) (smiOutput, error) {
		switch args[0] {
		case "--query-compute-apps=gpu_uuid,pid,process_name,used_memory":
			return smiOutput{
				Stdout: strings.Join([]string{
					"GPU-111, 1234, /usr/bin/python, 1024",
					"GPU-222, 4321, /opt/train, N/A",
				}, "\n"),
			}, nil
		case "--query-gpu=uuid,pci.bus_id":
			return smiOutput{
				Stdout: strings.Join([]string{
					"GPU-111, 00000000:01:00.0",
					"GPU-222, 00000000:02:00.0",
				}, "\n"),
			}, nil
		default:
			t.Fatalf("unexpected nvidia-smi args: %v", args)
			return smiOutput{}, nil
		}
	}}

	processes, err := client.QueryComputeProcessesContext(context.Background())
	if err != nil {
		t.Fatalf("QueryComputeProcessesContext returned error: %v", err)
	}
	if len(processes) != 2 {
		t.Fatalf("expected 2 processes, got %d", len(processes))
	}

	if processes[0].GPUUUID != "GPU-111" || processes[0].GPUPCIBusID != "00000000:01:00.0" {
		t.Fatalf("unexpected first process GPU identity: %+v", processes[0])
	}
	if processes[0].PID != 1234 || processes[0].ProcessName != "/usr/bin/python" {
		t.Fatalf("unexpected first process details: %+v", processes[0])
	}
	assertInt64Ptr(t, "first process memory", processes[0].UsedMemoryMiB, 1024)

	if processes[1].GPUUUID != "GPU-222" || processes[1].GPUPCIBusID != "00000000:02:00.0" {
		t.Fatalf("unexpected second process GPU identity: %+v", processes[1])
	}
	if processes[1].UsedMemoryMiB != nil {
		t.Fatalf("expected nil N/A memory, got %v", *processes[1].UsedMemoryMiB)
	}
}

func TestQueryComputeProcessesContextReturnsEmptyForNoProcesses(t *testing.T) {
	tests := []struct {
		name   string
		output smiOutput
		err    error
	}{
		{
			name: "empty success",
			output: smiOutput{
				Stdout: " \n",
			},
		},
		{
			name: "explicit no running error",
			output: smiOutput{
				Stderr: "No running compute processes found",
			},
			err: errors.New("exit status 1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := Client{run: func(ctx context.Context, args ...string) (smiOutput, error) {
				return tt.output, tt.err
			}}

			processes, err := client.QueryComputeProcessesContext(context.Background())
			if err != nil {
				t.Fatalf("QueryComputeProcessesContext returned error: %v", err)
			}
			if !reflect.DeepEqual(processes, []ComputeProcess{}) {
				t.Fatalf("expected empty process list, got %+v", processes)
			}
		})
	}
}

func TestQueryComputeProcessesContextPropagatesNvidiaSMIFailure(t *testing.T) {
	client := Client{run: func(ctx context.Context, args ...string) (smiOutput, error) {
		return smiOutput{
			Stderr: "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver.",
		}, errors.New("exit status 1")
	}}

	processes, err := client.QueryComputeProcessesContext(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if processes != nil {
		t.Fatalf("expected nil processes on failure, got %+v", processes)
	}
	if !strings.Contains(err.Error(), "query compute processes") {
		t.Fatalf("expected operation context in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "couldn't communicate") {
		t.Fatalf("expected nvidia-smi stderr in error, got %v", err)
	}
}

func assertFloat64Ptr(t *testing.T, label string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: expected %v, got nil", label, want)
	}
	if *got != want {
		t.Fatalf("%s: expected %v, got %v", label, want, *got)
	}
}

func assertInt64Ptr(t *testing.T, label string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: expected %v, got nil", label, want)
	}
	if *got != want {
		t.Fatalf("%s: expected %v, got %v", label, want, *got)
	}
}
