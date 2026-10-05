package featureflag

import "time"

// SetClock makes c, and its outage tracking, read the time from now.
func SetClock(c *OFREP, now func() time.Time) {
	c.now = now
	c.outage.now = now
}
