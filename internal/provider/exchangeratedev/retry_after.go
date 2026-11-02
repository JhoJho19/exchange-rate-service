package exchangeratedev

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func parseRetryAfter(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	// HTTP delay-seconds allows digits only, not signs or fractions.
	digits := true
	for _, c := range value {
		if c < '0' || c > '9' {
			digits = false
			break
		}
	}
	if digits {
		seconds, err := strconv.ParseUint(value, 10, 64)
		const maxSeconds = uint64((1<<63 - 1) / int64(time.Second))
		if err != nil || seconds > maxSeconds {
			return time.Time{}
		}
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(value); err == nil {
		return date
	}
	return time.Time{}
}
