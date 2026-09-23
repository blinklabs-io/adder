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

// Package nodeconn provides cancellable node connection setup.
package nodeconn

import (
	"context"
	"net"

	ouroboros "github.com/blinklabs-io/gouroboros"
)

// Dial cancels both the socket dial and the Ouroboros handshake with ctx.
// The caller owns the returned connection after a successful handshake.
func Dial(
	ctx context.Context,
	network, address string,
	options ...ouroboros.ConnectionOptionFunc,
) (*ouroboros.Connection, error) {
	dialer := net.Dialer{Timeout: ouroboros.DefaultConnectTimeout}
	raw, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	// NewConnection performs the handshake synchronously and has no
	// context API. Closing its transport interrupts that wait.
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = raw.Close()
		close(closed)
	})
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	options = append(options, ouroboros.WithConnection(raw))
	conn, err := ouroboros.NewConnection(options...)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
