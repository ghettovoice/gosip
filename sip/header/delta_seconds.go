package header

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

func parseDeltaSeconds(value string) (time.Duration, error) {
	seconds, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, errors.ErrorfWrap("invalid expiry value: %w", err)
	}
	return time.Duration(seconds) * time.Second, nil
}

func validDeltaSeconds(duration time.Duration) bool {
	return duration >= 0 && duration <= time.Duration(math.MaxUint32)*time.Second
}

func renderDeltaSecondsTo(w io.Writer, duration time.Duration) (int, error) {
	return errors.Wrap2(fmt.Fprint(w, int64(duration/time.Second)))
}

func renderDeltaSeconds(duration time.Duration) string {
	return strconv.FormatInt(int64(duration/time.Second), 10)
}
