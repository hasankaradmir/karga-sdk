package resource

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var memorySuffixes = []struct {
	suffix string
	factor float64
}{
	// Binary suffixes first so "Mi" is not read as "M" followed by "i".
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
	{"K", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
}

// ParseMemory converts "512Mi", "1G" or "1048576" to bytes.
func ParseMemory(s string) (int64, error) {
	num, factor := s, 1.0
	for _, m := range memorySuffixes {
		if strings.HasSuffix(s, m.suffix) {
			num, factor = strings.TrimSuffix(s, m.suffix), m.factor
			break
		}
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || !(f > 0) || math.IsInf(f, 0) || f*factor > math.MaxInt64 {
		return 0, fmt.Errorf("%q is not a valid memory quantity (e.g. 512Mi, 1Gi)", s)
	}
	return int64(f * factor), nil
}

// ParseCPU converts "1", "0.5" or "500m" to millicores.
func ParseCPU(s string) (int64, error) {
	num, factor := s, 1000.0
	if strings.HasSuffix(s, "m") {
		num, factor = strings.TrimSuffix(s, "m"), 1
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || !(f > 0) || math.IsInf(f, 0) || f*factor > math.MaxInt64 || f*factor < 1 {
		return 0, fmt.Errorf("%q is not a valid cpu quantity (e.g. 1, 0.5, 500m)", s)
	}
	return int64(f * factor), nil
}
