package agent

import "time"

func nowMillis() int64 { return time.Now().UnixMilli() }
