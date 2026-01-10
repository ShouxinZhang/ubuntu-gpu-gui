package gpu

import "strings"

func VendorName(vendorHex string) string {
	s := strings.ToLower(strings.TrimSpace(vendorHex))
	s = strings.TrimPrefix(s, "0x")
	s = "0x" + s
	switch s {
	case "0x10de":
		return "NVIDIA"
	case "0x1002":
		return "AMD"
	case "0x8086":
		return "Intel"
	default:
		return s
	}
}
