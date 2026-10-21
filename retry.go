package deepseek

import (
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func retryStatus(status int) bool {
	switch status {
	case 408, 429, 500, 502, 503, 504:
		return true
	}

	return false
}

// Honor Retry-After up to 30 seconds; otherwise stop instead of retrying early.
func retryDelay(header string, attempt int, now time.Time) (time.Duration, bool) {
	const maxDelay = 30 * time.Second
	if header = strings.TrimSpace(header); header != "" {
		if strings.Trim(header, "0123456789") == "" {
			seconds, err := strconv.ParseUint(header, 10, 64)
			if err != nil {
				// A syntactically valid delay can overflow uint64. It still
				// exceeds our retry budget; do not fall back to a short delay.
				return 0, false
			}

			if seconds > 30 {
				return 0, false
			}

			return time.Duration(seconds) * time.Second, true
		} else if when, err := http.ParseTime(header); err == nil {
			delay := when.Sub(now)
			if delay > maxDelay {
				return 0, false
			}

			if delay < 0 {
				delay = 0
			}

			return delay, true
		}
	}

	if attempt > 6 {
		attempt = 6
	}

	ceiling := 250 * time.Millisecond * time.Duration(1<<uint(attempt))
	return ceiling/2 + time.Duration(rand.Int63n(int64(ceiling/2)+1)), true
}
