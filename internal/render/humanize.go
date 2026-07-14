package render

import (
	"fmt"
	"strconv"
)

// Bytes renders a byte count with binary units and one decimal, the way
// humans compare log volumes ("1.2 MiB" beats "1258291").
func Bytes(n float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f B", n)
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

// Count renders an integer with comma thousands separators.
func Count(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg, s = true, s[1:]
	}
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var out []byte
	lead := len(s) % 3
	if lead > 0 {
		out = append(out, s[:lead]...)
	}
	for i := lead; i < len(s); i += 3 {
		if len(out) > 0 {
			out = append(out, ',')
		}
		out = append(out, s[i:i+3]...)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// USD renders a dollar amount: cents below $100, whole dollars above,
// so both $0.07 hobby projects and $12,400 enterprise bills read well.
func USD(v float64) string {
	if v < 100 {
		return fmt.Sprintf("$%.2f", v)
	}
	return "$" + Count(int64(v+0.5))
}
