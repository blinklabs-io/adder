// Copyright 2023 Blink Labs Software
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

package webhook

import (
	"time"

	"github.com/blinklabs-io/adder/plugin"
)

type WebhookOptionFunc func(*WebhookOutput)

// WithLogger specifies the logger object to use for logging messages
func WithLogger(logger plugin.Logger) WebhookOptionFunc {
	return func(o *WebhookOutput) {
		o.logger = logger
	}
}

// WithUrl specifies the webhook URL
func WithUrl(url string, skipVerify bool) WebhookOptionFunc {
	return func(o *WebhookOutput) {
		o.url = url
		o.skipVerify = skipVerify
	}
}

// WithBasicAuth specifies the username and password
func WithBasicAuth(username, password string) WebhookOptionFunc {
	return func(o *WebhookOutput) {
		o.username = username
		o.password = password
	}
}

// WithFormat specifies the output webhook format
func WithFormat(format string) WebhookOptionFunc {
	return func(o *WebhookOutput) {
		o.format = format
	}
}

// WithRetryConfig specifies the retry configuration for webhook delivery
func WithRetryConfig(maxRetries int, initialBackoff, maxBackoff time.Duration) WebhookOptionFunc {
	return func(o *WebhookOutput) {
		if maxRetries >= 0 {
			o.maxRetries = maxRetries
		}
		if initialBackoff > 0 {
			o.initialBackoff = initialBackoff
		}
		if maxBackoff > 0 {
			o.maxBackoff = maxBackoff
		}
	}
}
