package trader

import (
	"hash/fnv"
	"time"
)

const maxStartStaggerWindow = 3 * time.Minute

func computeStartStagger(id, name, userID string, interval time.Duration) (delay time.Duration, window time.Duration) {
	if interval <= 0 {
		return 0, 0
	}
	key := id
	if key == "" {
		key = name
	}
	if key == "" {
		key = userID
	}
	if key == "" {
		return 0, 0
	}

	window = interval
	if window > maxStartStaggerWindow {
		window = maxStartStaggerWindow
	}
	if window < time.Second {
		return 0, window
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	delay = time.Duration(h.Sum32()%uint32(window.Milliseconds())) * time.Millisecond
	return delay, window
}
