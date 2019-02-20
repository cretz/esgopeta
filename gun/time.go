package gun

import (
	"sync/atomic"
	"time"
)

// TimeFromUnixMs returns zero'd time if ms is 0
func TimeFromUnixMs(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.Unix(0, ms*int64(time.Millisecond))
}

// TimeToUnixMs returns 0 if t.IsZero
func TimeToUnixMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano() / int64(time.Millisecond)
}

func TimeNowUnixMs() int64 {
	return TimeToUnixMs(time.Now())
}

var lastNano int64

// uniqueNano is 0 if ms is first time seen, otherwise a unique num in combination with ms
func TimeNowUniqueUnix() (ms int64, uniqueNum int64) {
	now := time.Now()
	newNano := now.UnixNano()
	for {
		prevLastNano := lastNano
		if prevLastNano < newNano && atomic.CompareAndSwapInt64(&lastNano, prevLastNano, newNano) {
			ms = newNano / int64(time.Millisecond)
			// If was same ms as seen before, set uniqueNum to the nano part
			if prevLastNano/int64(time.Millisecond) == ms {
				uniqueNum = newNano%int64(time.Millisecond) + 1
			}
			return
		}
		newNano = prevLastNano + 1
	}
}
