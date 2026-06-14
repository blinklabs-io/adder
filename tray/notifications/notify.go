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

package notifications

import (
	"log/slog"

	"fyne.io/fyne/v2"
)

// Notifier is the minimal surface of fyne.App needed to dispatch a
// desktop notification. fyne.App satisfies it directly; tests inject a
// fake via fyne.io/fyne/v2/test's test.NewApp (which records sent
// notifications for test.AssertNotificationSent). This is the only file
// in the notifications package permitted to import fyne.
type Notifier interface {
	SendNotification(*fyne.Notification)
}

// Dispatch reads Requests from reqs and sends each as a native desktop
// notification. Returns when reqs is closed (Engine.Stop does this).
// Empty titles fall back to "Adder" so the OS doesn't drop them.
//
// currentEpoch drops Requests rendered against a superseded rule set
// (closes the SetRules-vs-in-flight race). nil disables the filter.
//
// recordDrop is invoked on each stale-epoch drop so dispatch-side
// losses feed the unified Stats().Dropped counter. nil is acceptable.
func Dispatch(
	reqs <-chan Request,
	n Notifier,
	currentEpoch func() int64,
	recordDrop func(),
) {
	for req := range reqs {
		if currentEpoch != nil && req.Epoch < currentEpoch() {
			if recordDrop != nil {
				recordDrop()
			}
			slog.Debug("notification dropped: stale epoch",
				"ruleID", req.RuleID,
				"reqEpoch", req.Epoch,
				"current", currentEpoch())
			continue
		}
		title := req.Title
		if title == "" {
			title = "Adder"
		}
		slog.Debug("dispatching native notification",
			"ruleID", req.RuleID,
			"title", title,
			"batched", req.Batched,
			"count", req.Count)
		n.SendNotification(fyne.NewNotification(title, req.Body))
	}
}
