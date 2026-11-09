// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package matrix

import (
	"github.com/blinklabs-io/adder/plugin"
	"net/http"
)

type Option func(*MatrixOutput)

func WithHomeserver(url string) Option    { return func(m *MatrixOutput) { m.homeserver = url } }
func WithAccessToken(token string) Option { return func(m *MatrixOutput) { m.token = token } }
func WithRooms(rooms ...string) Option {
	return func(m *MatrixOutput) { m.rooms = append([]string(nil), rooms...) }
}
func WithEventTypes(types map[string]bool) Option {
	return func(m *MatrixOutput) { m.selected = types }
}
func WithHTTPClient(client *http.Client) Option { return func(m *MatrixOutput) { m.client = client } }
func WithMaxRetries(n int) Option {
	return func(m *MatrixOutput) {
		if n >= 0 && n <= 3 {
			m.maxRetries = n
		}
	}
}
func WithLogger(logger plugin.Logger) Option { return func(m *MatrixOutput) { m.SetLogger(logger) } }
