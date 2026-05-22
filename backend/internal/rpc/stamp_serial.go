package rpc

import (
	"fmt"
	"strconv"
)

// parseStampSerial splits a CRA excise stamp serial into its alpha prefix
// and trailing numeric portion. CRA stamps are typically 3 or 4 letters
// followed by 5 or 6 digits, e.g., "ABC00001" → ("ABC", 1, 5). If the
// input has no trailing digits, num = 0 and padWidth = 0.
func parseStampSerial(s string) (prefix string, num int64, padWidth int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) {
		return s, 0, 0
	}
	prefix = s[:i]
	num, _ = strconv.ParseInt(s[i:], 10, 64)
	padWidth = len(s) - i
	return
}

// formatStampSerial reconstructs a serial from prefix + number, padding
// numerics to padWidth.
func formatStampSerial(prefix string, num int64, padWidth int) string {
	if padWidth <= 0 {
		return prefix
	}
	return fmt.Sprintf("%s%0*d", prefix, padWidth, num)
}

// computeStampRange returns the (start, end) serial strings for a slice
// of stamps starting at offset `priorApplied` within an order whose
// serial range begins at orderStartSerial. Returns empty strings when
// orderStartSerial isn't a parseable format.
func computeStampRange(orderStartSerial string, priorApplied int32, takeCount int32) (start, end string) {
	if orderStartSerial == "" || takeCount <= 0 {
		return "", ""
	}
	prefix, base, pad := parseStampSerial(orderStartSerial)
	if pad == 0 {
		return "", ""
	}
	first := base + int64(priorApplied)
	last := first + int64(takeCount) - 1
	return formatStampSerial(prefix, first, pad), formatStampSerial(prefix, last, pad)
}
