// Package timerlimit defines timer duration bounds shared across the runtime.
package timerlimit

import "time"

// MaxDuration is the maximum accepted or interactively adjusted timer total.
const MaxDuration = 30 * 24 * time.Hour
