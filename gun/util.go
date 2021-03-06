package gun

import (
	"crypto/rand"
	"sync/atomic"
	"time"
)

const randChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func randString(n int) (s string) {
	// We accept that a multiple of 64 is %'d on 62 potentially favoring 0 or 1 more, but we don't care
	byts := make([]byte, n)
	if _, err := rand.Read(byts); err != nil {
		panic(err)
	}
	for _, byt := range byts {
		s += string(randChars[int(byt)%len(randChars)])
	}
	return s
}

// timeFromUnixMs returns zero'd time if ms is 0
func timeFromUnixMs(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.Unix(0, ms*int64(time.Millisecond))
}

// timeToUnixMs returns 0 if t.IsZero
func timeToUnixMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano() / int64(time.Millisecond)
}

func timeNowUnixMs() int64 {
	return timeToUnixMs(time.Now())
}

var lastNano int64

// uniqueNano is 0 if ms is first time seen, otherwise a unique num in combination with ms
func timeNowUniqueUnix() (ms int64, uniqueNum int64) {
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
