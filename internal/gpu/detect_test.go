package gpu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLSPCIOutputParsesGPUIdentity(t *testing.T) {
	output := `
00:02.0 VGA compatible controller [0300]: Intel Corporation Alder Lake-P Integrated Graphics Controller [8086:46a6] (rev 0c)
0000:01:00.0 3D controller [0302]: NVIDIA Corporation GA107M [GeForce RTX 3050 Mobile] [10de:25a2] (rev a1)
00:1f.3 Audio device [0403]: Intel Corporation Alder Lake PCH-P High Definition Audio Controller [8086:51c8] (rev 01)
02:00.0 Display controller: Advanced Micro Devices, Inc. [AMD/ATI] Rembrandt [1002:1681] (rev c5)
`

	devices, err := ParseLSPCIOutput(output)
	if err != nil {
		t.Fatalf("ParseLSPCIOutput returned error: %v", err)
	}
	if len(devices) != 3 {
		t.Fatalf("expected 3 GPU devices, got %d: %+v", len(devices), devices)
	}

	tests := []struct {
		name       string
		index      int
		busID      string
		className  string
		classHex   string
		vendorName string
		vendorHex  string
		deviceHex  string
		kind       Kind
	}{
		{
			name:       "intel domain-normalized VGA",
			index:      0,
			busID:      "0000:00:02.0",
			className:  "VGA compatible controller",
			classHex:   "0x0300",
			vendorName: "Intel",
			vendorHex:  "0x8086",
			deviceHex:  "0x46a6",
			kind:       KindIntegrated,
		},
		{
			name:       "nvidia 3D controller",
			index:      1,
			busID:      "0000:01:00.0",
			className:  "3D controller",
			classHex:   "0x0302",
			vendorName: "NVIDIA",
			vendorHex:  "0x10de",
			deviceHex:  "0x25a2",
			kind:       KindDiscrete,
		},
		{
			name:       "amd display controller without class id",
			index:      2,
			busID:      "0000:02:00.0",
			className:  "Display controller",
			classHex:   "",
			vendorName: "AMD",
			vendorHex:  "0x1002",
			deviceHex:  "0x1681",
			kind:       KindUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := devices[tt.index]
			if device.BusID != tt.busID {
				t.Fatalf("BusID = %q, want %q", device.BusID, tt.busID)
			}
			if device.ClassName != tt.className {
				t.Fatalf("ClassName = %q, want %q", device.ClassName, tt.className)
			}
			if device.ClassHex != tt.classHex {
				t.Fatalf("ClassHex = %q, want %q", device.ClassHex, tt.classHex)
			}
			if device.VendorName != tt.vendorName {
				t.Fatalf("VendorName = %q, want %q", device.VendorName, tt.vendorName)
			}
			if device.VendorHex != tt.vendorHex {
				t.Fatalf("VendorHex = %q, want %q", device.VendorHex, tt.vendorHex)
			}
			if device.DeviceHex != tt.deviceHex {
				t.Fatalf("DeviceHex = %q, want %q", device.DeviceHex, tt.deviceHex)
			}
			if device.Kind != tt.kind {
				t.Fatalf("Kind = %q, want %q", device.Kind, tt.kind)
			}
		})
	}
}

func TestNormalizePCIAddress(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{name: "adds default domain", input: "01:00.0", want: "0000:01:00.0", wantOK: true},
		{name: "preserves explicit domain", input: "0001:02:03.4", want: "0001:02:03.4", wantOK: true},
		{name: "lowercases", input: "ABCD:0A:0B.1", want: "abcd:0a:0b.1", wantOK: true},
		{name: "rejects non pci address", input: "card0", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NormalizePCIAddress(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyDeviceEdgeCases(t *testing.T) {
	bootFalse := false

	tests := []struct {
		name   string
		device Device
		want   Kind
	}{
		{name: "nvidia is discrete", device: Device{ClassHex: "0x0300", VendorHex: "0x10de"}, want: KindDiscrete},
		{name: "intel VGA defaults integrated", device: Device{ClassHex: "0x0300", VendorHex: "0x8086"}, want: KindIntegrated},
		{name: "intel non-boot VGA stays unknown", device: Device{ClassHex: "0x0300", VendorHex: "0x8086", BootVGA: &bootFalse}, want: KindUnknown},
		{name: "intel 3D controller is discrete before intel integrated fallback", device: Device{ClassHex: "0x0302", VendorHex: "0x8086"}, want: KindDiscrete},
		{name: "amd VGA is unknown without guessing from other devices", device: Device{ClassHex: "0x0300", VendorHex: "0x1002"}, want: KindUnknown},
		{name: "unknown 3D controller is discrete", device: Device{ClassName: "3D controller", VendorHex: "0x1a03"}, want: KindDiscrete},
		{name: "unknown VGA is unknown", device: Device{ClassHex: "0x0300", VendorHex: "0x1a03"}, want: KindUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyDevice(tt.device); got != tt.want {
				t.Fatalf("ClassifyDevice() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadDRMDevicesFromTempSysfs(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "class", "drm")
	devicePath := filepath.Join(root, "devices", "pci0000:00", "0000:00:02.0")
	cardPath := filepath.Join(base, "card0")

	mkdirAll(t, devicePath)
	mkdirAll(t, cardPath)
	writeFile(t, filepath.Join(devicePath, "vendor"), "0X8086\n")
	writeFile(t, filepath.Join(devicePath, "device"), "46A6\n")
	writeFile(t, filepath.Join(devicePath, "class"), "0x030000\n")
	writeFile(t, filepath.Join(devicePath, "boot_vga"), "1\n")
	writeFile(t, filepath.Join(devicePath, "gt_busy_percent"), "37\n")
	writeFile(t, filepath.Join(devicePath, "gpu_busy_percent"), "82\n")
	if err := os.Symlink(devicePath, filepath.Join(cardPath, "device")); err != nil {
		t.Fatalf("create device symlink: %v", err)
	}

	mkdirAll(t, filepath.Join(base, "card0-DP-1"))

	devices, err := ReadDRMDevices(base)
	if err != nil {
		t.Fatalf("ReadDRMDevices returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected one DRM card, got %d: %+v", len(devices), devices)
	}

	device := devices[0]
	if device.Card != "card0" {
		t.Fatalf("Card = %q, want card0", device.Card)
	}
	if device.BusID != "0000:00:02.0" {
		t.Fatalf("BusID = %q, want 0000:00:02.0", device.BusID)
	}
	if device.VendorHex != "0x8086" {
		t.Fatalf("VendorHex = %q, want 0x8086", device.VendorHex)
	}
	if device.DeviceHex != "0x46a6" {
		t.Fatalf("DeviceHex = %q, want 0x46a6", device.DeviceHex)
	}
	if device.ClassHex != "0x030000" {
		t.Fatalf("ClassHex = %q, want 0x030000", device.ClassHex)
	}
	if device.BootVGA == nil || !*device.BootVGA {
		t.Fatalf("BootVGA = %v, want true", device.BootVGA)
	}
	if device.BusyPct == nil || *device.BusyPct != 82 {
		t.Fatalf("BusyPct = %v, want 82", device.BusyPct)
	}
	if device.BusySource != "gpu_busy_percent" {
		t.Fatalf("BusySource = %q, want gpu_busy_percent", device.BusySource)
	}
}

func TestVendorNormalization(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantNormalized string
		wantName       string
	}{
		{name: "intel without prefix", input: " 8086\n", wantNormalized: "0x8086", wantName: "Intel"},
		{name: "nvidia uppercase prefix", input: "0X10DE", wantNormalized: "0x10de", wantName: "NVIDIA"},
		{name: "amd bracketed", input: "[1002]", wantNormalized: "0x1002", wantName: "AMD"},
		{name: "unknown normalized", input: "1A03", wantNormalized: "0x1a03", wantName: "0x1a03"},
		{name: "empty", input: " \n", wantNormalized: "", wantName: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeVendorHex(tt.input); got != tt.wantNormalized {
				t.Fatalf("NormalizeVendorHex() = %q, want %q", got, tt.wantNormalized)
			}
			if got := VendorName(tt.input); got != tt.wantName {
				t.Fatalf("VendorName() = %q, want %q", got, tt.wantName)
			}
		})
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
