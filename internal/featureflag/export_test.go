package featureflag

import (
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/outage"
)

// SetClock makes c, and its outage tracking, read the time from now.
func SetClock(c *OFREP, now func() time.Time) {
	c.now = now
	c.outage = outage.New(now)
}
