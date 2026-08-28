// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package notifyjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/tray/notifications"
	"github.com/blinklabs-io/adder/tray/setup"
)

const schemaVersion = 1

type statusRecord struct {
	SchemaVersion int       `json:"schemaVersion"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	Timestamp     time.Time `json:"timestamp"`
	Message       string    `json:"message,omitempty"`
}

type notificationRecord struct {
	SchemaVersion int       `json:"schemaVersion"`
	Kind          string    `json:"kind"`
	Timestamp     time.Time `json:"timestamp"`
	RuleID        string    `json:"ruleId"`
	EventType     string    `json:"eventType,omitempty"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	Batched       bool      `json:"batched"`
	Count         int       `json:"count"`
}

type Output struct {
	configPath         string
	writer             io.Writer
	staleAfter         time.Duration
	staleAfterOverride time.Duration

	eventChan chan event.Event
	errorChan chan error
	done      chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	writeMu   sync.Mutex
	engine    *notifications.Engine
	config    setup.NotificationConfig
}

func New(options ...Option) *Output {
	o := &Output{writer: os.Stdout}
	for _, option := range options {
		option(o)
	}
	return o
}

func (o *Output) Start() error {
	if o.configPath == "" {
		return errors.New("notify-json config path must not be empty")
	}
	cfg, err := setup.ReadNotificationConfig(o.configPath)
	if err != nil {
		return err
	}
	o.config = cfg
	o.staleAfter = o.staleAfterOverride
	if o.staleAfter <= 0 {
		o.staleAfter = time.Duration(cfg.ConnectionStaleSeconds) * time.Second
	}
	o.eventChan = make(chan event.Event, 64)
	o.errorChan = make(chan error, 8)
	o.done = make(chan struct{})
	o.stopOnce = sync.Once{}

	engineEvents := make(chan event.Event, 64)
	plan := cfg.SetupPlan()
	o.engine = notifications.NewEngine(
		engineEvents,
		notifications.RulesFromPlan(plan),
		notifications.WithRateLimit(
			plan.App.NotifyRateLimit,
			plan.App.NotifyRateWindow,
		),
	)
	o.engine.Start()
	if err := o.writeRecord(statusRecord{
		SchemaVersion: schemaVersion,
		Kind:          "status",
		Status:        "starting",
		Timestamp:     time.Now().UTC(),
		Message:       "waiting for the first chain event",
	}); err != nil {
		o.engine.Stop()
		return fmt.Errorf("writing initial status: %w", err)
	}

	o.wg.Add(2)
	go o.eventLoop(engineEvents)
	go o.requestLoop()
	return nil
}

func (o *Output) eventLoop(engineEvents chan<- event.Event) {
	defer o.wg.Done()
	defer close(engineEvents)

	timer := time.NewTimer(o.staleAfter)
	defer timer.Stop()
	connected := false
	everConnected := false

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(o.staleAfter)
	}

	for {
		select {
		case <-o.done:
			return
		case evt := <-o.eventChan:
			if !connected {
				message := "receiving chain events from " + o.config.NetworkLabel()
				o.reportWriteError(o.writeRecord(statusRecord{
					SchemaVersion: schemaVersion,
					Kind:          "status",
					Status:        "connected",
					Timestamp:     time.Now().UTC(),
					Message:       message,
				}))
				if everConnected {
					o.engine.NotifyConnection(
						"Adder Connection",
						"Reconnected to "+o.config.NetworkLabel(),
					)
				}
				connected = true
				everConnected = true
			}
			resetTimer()
			normalized, err := normalizeEvent(evt)
			if err != nil {
				o.reportError(fmt.Errorf(
					"normalizing event for notification rules: %w",
					err,
				))
				continue
			}
			select {
			case engineEvents <- normalized:
			case <-o.done:
				return
			}
		case <-timer.C:
			message := "no chain events received before startup timeout"
			if connected {
				message = "no chain events received recently"
			}
			o.reportWriteError(o.writeRecord(statusRecord{
				SchemaVersion: schemaVersion,
				Kind:          "status",
				Status:        "stale",
				Timestamp:     time.Now().UTC(),
				Message:       message,
			}))
			if connected {
				connected = false
				o.engine.NotifyConnection(
					"Adder Connection",
					"Lost connection to "+o.config.NetworkLabel(),
				)
			}
		}
	}
}

// normalizeEvent gives typed ChainSync events the same shape as events decoded
// from adder-tray's JSON WebSocket. Already-normalized events take the fast
// path; native event structs use a JSON round-trip to preserve their stable
// field names and the map/slice shapes expected by target matchers.
func normalizeEvent(evt event.Event) (event.Event, error) {
	if isNormalizedJSON(evt.Context) && isNormalizedJSON(evt.Payload) {
		return evt, nil
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return event.Event{}, err
	}
	var normalized event.Event
	if err := json.Unmarshal(data, &normalized); err != nil {
		return event.Event{}, err
	}
	return normalized, nil
}

func isNormalizedJSON(value any) bool {
	switch value := value.(type) {
	case nil, bool, float64, string:
		return true
	case []any:
		for _, item := range value {
			if !isNormalizedJSON(item) {
				return false
			}
		}
		return true
	case map[string]any:
		for _, item := range value {
			if !isNormalizedJSON(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (o *Output) requestLoop() {
	defer o.wg.Done()
	for req := range o.engine.Requests() {
		if req.Epoch < o.engine.CurrentEpoch() {
			o.engine.RecordDrop()
			continue
		}
		title := req.Title
		if title == "" {
			title = "Adder"
		}
		o.reportWriteError(o.writeRecord(notificationRecord{
			SchemaVersion: schemaVersion,
			Kind:          "notification",
			Timestamp:     time.Now().UTC(),
			RuleID:        req.RuleID,
			EventType:     req.Event.Type,
			Title:         title,
			Body:          req.Body,
			Batched:       req.Batched,
			Count:         req.Count,
		}))
	}
}

func (o *Output) writeRecord(record any) error {
	o.writeMu.Lock()
	defer o.writeMu.Unlock()
	return json.NewEncoder(o.writer).Encode(record)
}

func (o *Output) reportWriteError(err error) {
	if err == nil {
		return
	}
	o.reportError(fmt.Errorf("writing notify-json record: %w", err))
}

func (o *Output) reportError(err error) {
	select {
	case o.errorChan <- err:
	default:
	}
}

func (o *Output) Stop() error {
	if o.done == nil {
		return nil
	}
	o.stopOnce.Do(func() { close(o.done) })
	if o.engine != nil {
		o.engine.Stop()
	}
	o.wg.Wait()
	if o.errorChan != nil {
		close(o.errorChan)
	}
	o.eventChan = nil
	o.errorChan = nil
	o.done = nil
	return nil
}

func (o *Output) ErrorChan() <-chan error        { return o.errorChan }
func (o *Output) InputChan() chan<- event.Event  { return o.eventChan }
func (o *Output) OutputChan() <-chan event.Event { return nil }
