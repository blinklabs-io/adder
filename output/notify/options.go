// Copyright 2023 Blink Labs, LLC.
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

package notify

import "github.com/blinklabs-io/snek/plugin"

// import "github.com/blinklabs-io/snek/event"

type NotifyOptionFunc func(*NotifyOutput)

// WithLogger specifies the logger object to use for logging messages
func WithLogger(logger plugin.Logger) NotifyOptionFunc {
	return func(o *NotifyOutput) {
		o.logger = logger
	}
}

// WithTitle specifies the notification title
func WithTitle(title string) NotifyOptionFunc {
	return func(o *NotifyOutput) {
		o.title = title
	}
}
