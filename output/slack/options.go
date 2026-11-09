// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package slack

import (
	"net/http"

	"github.com/blinklabs-io/adder/plugin"
)

// Option configures SlackOutput.
type Option func(*SlackOutput)

// WithWebhookURLs sets the Slack destinations. Each URL carries its own authentication secret.
func WithWebhookURLs(urls ...string) Option {
	return func(s *SlackOutput) { s.urls = append([]string(nil), urls...) }
}

// WithEventTypes sets the enabled event types.
func WithEventTypes(types map[string]bool) Option {
	return func(s *SlackOutput) {
		s.selected = make(map[string]bool, len(types))
		for typ, enabled := range types {
			s.selected[typ] = enabled
		}
	}
}

// WithHTTPClient sets the HTTP client used to deliver messages.
func WithHTTPClient(client *http.Client) Option {
	return func(s *SlackOutput) { s.client = client }
}

// WithLogger sets the plugin logger.
func WithLogger(logger plugin.Logger) Option {
	return func(s *SlackOutput) { s.SetLogger(logger) }
}

// WithMaxRetries sets zero to three retries after the first delivery attempt.
func WithMaxRetries(retries int) Option {
	return func(s *SlackOutput) {
		if retries >= 0 && retries <= 3 {
			s.maxRetries = retries
		}
	}
}
