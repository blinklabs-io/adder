// Copyright 2025 Blink Labs Software
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

package api

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestAPIStartReturnsBindError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := uint(occupied.Addr().(*net.TCPAddr).Port)
	server := &APIv1{
		engine: ConfigureRouter(false),
		Host:   "127.0.0.1",
		Port:   port,
	}

	if err := server.Start(); err == nil {
		t.Fatal("expected occupied port to fail synchronously")
	}
}

func TestAPIShutdownReleasesListener(t *testing.T) {
	server := &APIv1{
		engine: ConfigureRouter(false),
		Host:   "127.0.0.1",
		Port:   0,
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	address := server.listener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("listener still accepted connections after shutdown")
	}
}
