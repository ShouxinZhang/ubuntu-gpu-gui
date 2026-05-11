package gpu

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ShouxinZhang/ubuntu-gpu-gui/internal/system"
)

const detectTimeout = 2 * time.Second
const sysfsDRMBasePath = "/sys/class/drm"

var (
	pciAddressRE = regexp.MustCompile(`^(?:(?:[[:xdigit:]]{4}):)?[[:xdigit:]]{2}:[[:xdigit:]]{2}\.[0-7]$`)
	pciIDPairRE  = regexp.MustCompile(`\[([[:xdigit:]]{4}):([[:xdigit:]]{4})\]`)
	classIDRE    = regexp.MustCompile(`\[([[:xdigit:]]{4})\]`)
	revisionRE   = regexp.MustCompile(`\s*\(rev [^)]+\)`)
)

type Kind string

const (
	KindUnknown    Kind = "unknown"
	KindIntegrated Kind = "iGPU"
	KindDiscrete   Kind = "dGPU"
)

type Device struct {
	Kind       Kind
	Name       string
	BusID      string // domain-normalized PCI bus id, e.g. 0000:01:00.0
	ClassName  string
	ClassHex   string
	VendorName string
	VendorHex  string
	DeviceName string
	DeviceHex  string
	BootVGA    *bool
}

// Detect returns a best-effort list of GPU devices from lspci.
// This does not provide utilization metrics; it's for labeling iGPU vs dGPU.
func Detect() ([]Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), detectTimeout)
	defer cancel()
	return DetectContext(ctx)
}

func DetectContext(ctx context.Context) ([]Device, error) {
	out, err := system.Run(ctx, "lspci", "-Dnn")
	if err != nil {
		out, err = system.Run(ctx, "lspci")
		if err != nil {
			return nil, err
		}
	}
	return ParseLSPCIOutput(out)
}

func Summarize(devices []Device) (integrated []string, discrete []string) {
	for _, d := range devices {
		switch d.Kind {
		case KindIntegrated:
			integrated = append(integrated, d.Name)
		case KindDiscrete:
			discrete = append(discrete, d.Name)
		}
	}
	return integrated, discrete
}

type DRMDevice struct {
	Card       string // e.g. card2
	BusID      string // domain-normalized PCI bus id, e.g. 0000:01:00.0
	VendorHex  string // e.g. 0x1002
	DeviceHex  string // e.g. 0x73df
	ClassHex   string // sysfs PCI class, e.g. 0x030000
	BootVGA    *bool
	BusyPct    *int   // nil if not available
	BusySource string // gpu_busy_percent / gt_busy_percent
}

// ParseLSPCIOutput parses plain or -Dnn lspci output without touching the host.
func ParseLSPCIOutput(output string) ([]Device, error) {
	var devices []Device
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		device, ok := ParseLSPCILine(scanner.Text())
		if !ok || !isGPUClass(device) {
			continue
		}
		device.Kind = ClassifyDevice(device)
		devices = append(devices, device)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}

// ParseLSPCILine parses one lspci row. ok is false when the row is not lspci-shaped.
func ParseLSPCILine(line string) (Device, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Device{}, false
	}

	busToken, rest, ok := strings.Cut(line, " ")
	if !ok {
		return Device{Name: line, Kind: KindUnknown}, false
	}
	busID, ok := NormalizePCIAddress(busToken)
	if !ok {
		return Device{Name: line, Kind: KindUnknown}, false
	}

	classPart, description, ok := strings.Cut(strings.TrimSpace(rest), ":")
	if !ok {
		return Device{Name: line, BusID: busID, Kind: KindUnknown}, false
	}

	classHex := ""
	if match := classIDRE.FindStringSubmatch(classPart); len(match) == 2 {
		classHex = normalizePCIHexID(match[1])
	}
	className := strings.TrimSpace(classIDRE.ReplaceAllString(classPart, ""))
	description = strings.TrimSpace(description)

	vendorHex, deviceHex := "", ""
	if match := pciIDPairRE.FindStringSubmatch(description); len(match) == 3 {
		vendorHex = NormalizeVendorHex(match[1])
		deviceHex = normalizePCIHexID(match[2])
	}

	deviceName := strings.TrimSpace(pciIDPairRE.ReplaceAllString(description, ""))
	deviceName = strings.TrimSpace(revisionRE.ReplaceAllString(deviceName, ""))
	deviceName = strings.Join(strings.Fields(deviceName), " ")

	vendorName := ""
	if name, ok := KnownVendorName(vendorHex); ok {
		vendorName = name
	} else {
		vendorName = inferVendorName(deviceName)
		if vendorName == "" && vendorHex != "" {
			vendorName = vendorHex
		}
	}

	device := Device{
		Kind:       KindUnknown,
		Name:       line,
		BusID:      busID,
		ClassName:  className,
		ClassHex:   classHex,
		VendorName: vendorName,
		VendorHex:  vendorHex,
		DeviceName: deviceName,
		DeviceHex:  deviceHex,
	}
	device.Kind = ClassifyDevice(device)
	return device, true
}

// NormalizePCIAddress returns a lower-case, domain-qualified PCI address.
func NormalizePCIAddress(address string) (string, bool) {
	address = strings.ToLower(strings.TrimSpace(address))
	if !pciAddressRE.MatchString(address) {
		return "", false
	}
	if strings.Count(address, ":") == 1 {
		address = "0000:" + address
	}
	return address, true
}

// ClassifyDevice returns a per-device kind without using system-wide GPU guesses.
func ClassifyDevice(device Device) Kind {
	if isNvidiaDevice(device) {
		return KindDiscrete
	}
	if is3DController(device) {
		return KindDiscrete
	}
	if isIntelDevice(device) {
		if device.BootVGA != nil && !*device.BootVGA {
			return KindUnknown
		}
		return KindIntegrated
	}
	return KindUnknown
}

// QueryDRMDevices reads best-effort GPU utilization from Linux DRM sysfs.
func QueryDRMDevices() ([]DRMDevice, error) {
	return ReadDRMDevices(sysfsDRMBasePath)
}

// ReadDRMDevices reads DRM GPU data from a sysfs-like base path.
func ReadDRMDevices(basePath string) ([]DRMDevice, error) {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" {
		basePath = sysfsDRMBasePath
	}

	entries, err := os.ReadDir(basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []DRMDevice
	for _, entry := range entries {
		card := entry.Name()
		if !isDRMCardName(card) {
			continue
		}

		cardPath := filepath.Join(basePath, card)
		devicePath, busID := resolveDRMDevicePath(cardPath)
		vendorPath := filepath.Join(devicePath, "vendor")
		vendorB, err := os.ReadFile(vendorPath)
		if err != nil {
			continue
		}
		vendorHex := NormalizeVendorHex(string(vendorB))

		deviceHex := ""
		if deviceB, err := os.ReadFile(filepath.Join(devicePath, "device")); err == nil {
			deviceHex = normalizePCIHexID(string(deviceB))
		}

		classHex := ""
		if classB, err := os.ReadFile(filepath.Join(devicePath, "class")); err == nil {
			classHex = normalizePCIHexID(string(classB))
		}

		bootVGA, _ := readBoolFile(filepath.Join(devicePath, "boot_vga"))

		var busy *int
		busySource := ""
		if v, err := readIntFile(filepath.Join(devicePath, "gpu_busy_percent")); err == nil {
			busy = &v
			busySource = "gpu_busy_percent"
		} else if v, err := readIntFile(filepath.Join(devicePath, "gt_busy_percent")); err == nil {
			busy = &v
			busySource = "gt_busy_percent"
		}

		out = append(out, DRMDevice{
			Card:       card,
			BusID:      busID,
			VendorHex:  vendorHex,
			DeviceHex:  deviceHex,
			ClassHex:   classHex,
			BootVGA:    bootVGA,
			BusyPct:    busy,
			BusySource: busySource,
		})
	}
	return out, nil
}

func VendorName(vendorHex string) string {
	if name, ok := KnownVendorName(vendorHex); ok {
		return name
	}
	if normalized := NormalizeVendorHex(vendorHex); normalized != "" {
		return normalized
	}
	return ""
}

func NormalizeVendorHex(vendorHex string) string {
	return normalizePCIHexID(vendorHex)
}

func KnownVendorName(vendorHex string) (string, bool) {
	switch NormalizeVendorHex(vendorHex) {
	case "0x10de":
		return "NVIDIA", true
	case "0x1002", "0x1022":
		return "AMD", true
	case "0x8086":
		return "Intel", true
	default:
		return "", false
	}
}

func isGPUClass(device Device) bool {
	switch strings.ToLower(device.ClassHex) {
	case "0x0300", "0x0302", "0x0380":
		return true
	}

	className := strings.ToLower(device.ClassName)
	return strings.Contains(className, "vga compatible controller") ||
		strings.Contains(className, "3d controller") ||
		strings.Contains(className, "display controller")
}

func isNvidiaDevice(device Device) bool {
	return device.VendorHex == "0x10de" || strings.Contains(deviceIdentityText(device), "nvidia")
}

func isIntelDevice(device Device) bool {
	return device.VendorHex == "0x8086" || strings.Contains(deviceIdentityText(device), "intel")
}

func is3DController(device Device) bool {
	return strings.EqualFold(device.ClassHex, "0x0302") ||
		strings.Contains(strings.ToLower(device.ClassName), "3d controller")
}

func deviceIdentityText(device Device) string {
	return strings.ToLower(strings.Join([]string{
		device.VendorName,
		device.DeviceName,
		device.Name,
	}, " "))
}

func inferVendorName(text string) string {
	text = strings.ToLower(text)
	switch {
	case strings.Contains(text, "nvidia"):
		return "NVIDIA"
	case strings.Contains(text, "intel"):
		return "Intel"
	case strings.Contains(text, "advanced micro devices"),
		strings.Contains(text, "amd/ati"),
		strings.Contains(text, "ati technologies"):
		return "AMD"
	default:
		return ""
	}
}

func resolveDRMDevicePath(cardPath string) (devicePath string, busID string) {
	devicePath = filepath.Join(cardPath, "device")
	resolved, err := filepath.EvalSymlinks(devicePath)
	if err == nil {
		devicePath = resolved
	}
	if normalized, ok := NormalizePCIAddress(filepath.Base(devicePath)); ok {
		busID = normalized
	}
	return devicePath, busID
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

func readBoolFile(path string) (*bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(string(b)) {
	case "0":
		v := false
		return &v, nil
	case "1":
		v := true
		return &v, nil
	default:
		return nil, fmt.Errorf("parse %s: expected 0 or 1", path)
	}
}

func isDRMCardName(name string) bool {
	if !strings.HasPrefix(name, "card") || len(name) == len("card") {
		return false
	}
	for _, r := range name[len("card"):] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizePCIHexID(hexID string) string {
	s := strings.ToLower(strings.TrimSpace(hexID))
	s = strings.Trim(s, "[]")
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return ""
	}
	return "0x" + s
}
