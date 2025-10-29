package blokfall

import "time"

// Gravity per level: milliseconds per row
var gravityByLevel = [30]time.Duration{
	800 * time.Millisecond, // Level 0
	700 * time.Millisecond,
	600 * time.Millisecond,
	500 * time.Millisecond,
	400 * time.Millisecond,
	300 * time.Millisecond,
	250 * time.Millisecond,
	200 * time.Millisecond,
	150 * time.Millisecond,
	100 * time.Millisecond, // level 9
}

func GravityByLevel(lv int) time.Duration {
	if lv > len(gravityByLevel) {
		lv = len(gravityByLevel) - 1
	}

	return gravityByLevel[lv]
}
