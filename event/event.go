package event

import (
	"time"
)

type Event struct {
	Type      string      `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   interface{} `json:"payload"`
}

func New(eventType string, timestamp time.Time, payload interface{}) Event {
	return Event{
		Type:      eventType,
		Timestamp: timestamp,
		Payload:   payload,
	}
}
