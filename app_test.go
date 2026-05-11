package main

import (
	"testing"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/gpu"
	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/nvidia"
)

func TestShortenLspci(t *testing.T) {
	t.Parallel()

	got := shortenLspci("01:00.0 3D controller: NVIDIA Corporation GA107M [GeForce RTX 3050 Mobile] (rev a1)")
	want := "01:00.0 NVIDIA Corporation GA107M [GeForce RTX 3050 Mobile] (rev a1)"
	if got != want {
		t.Fatalf("shortenLspci() = %q, want %q", got, want)
	}
}

func TestFormatIGPUUsesBusIDMatchedDRMUtilization(t *testing.T) {
	t.Parallel()

	busy := 37
	got := formatIGPU(
		[]gpu.Device{
			{
				Kind:       gpu.KindIntegrated,
				BusID:      "0000:00:02.0",
				DeviceName: "Intel Corporation Alder Lake-P Integrated Graphics Controller",
			},
		},
		[]gpu.DRMDevice{
			{Card: "card1", BusID: "0000:01:00.0", VendorHex: "0x10de"},
			{Card: "card0", BusID: "0000:00:02.0", VendorHex: "0x8086", BusyPct: &busy},
		},
	)
	want := "iGPU: 0000:00:02.0 Intel Corporation Alder Lake-P Integrated Graphics Controller: 37%"
	if got != want {
		t.Fatalf("formatIGPU() = %q, want %q", got, want)
	}
}

func TestFormatProcessListIncludesGPUIdentity(t *testing.T) {
	t.Parallel()

	mem := int64(1024)
	got := formatProcessList([]nvidia.ComputeProcess{
		{
			GPUPCIBusID:   "00000000:01:00.0",
			PID:           1234,
			ProcessName:   "/usr/bin/python",
			UsedMemoryMiB: &mem,
		},
	})
	want := "00000000:01:00.0: 1234, /usr/bin/python, 1024 MiB"
	if got != want {
		t.Fatalf("formatProcessList() = %q, want %q", got, want)
	}
}

func TestFormatFloatPtr(t *testing.T) {
	t.Parallel()

	value := 42.4
	if got := formatFloatPtr(&value, 0); got != "42" {
		t.Fatalf("formatFloatPtr() = %q, want 42", got)
	}
	if got := formatFloatPtr(nil, 0); got != "N/A" {
		t.Fatalf("formatFloatPtr(nil) = %q, want N/A", got)
	}
}
