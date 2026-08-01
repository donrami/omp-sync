package sync

import (
	"encoding/json"
	"time"
)

// nowUTC returns the current time in UTC.
func nowUTC() time.Time {
	return time.Now().UTC()
}

// decodeJSON is a tiny wrapper around json.Unmarshal to keep imports
// tidy in callers.
func decodeJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
