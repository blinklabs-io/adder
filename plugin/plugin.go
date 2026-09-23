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

package plugin

import (
	"context"

	"github.com/blinklabs-io/adder/event"
)

// Lifecycle governs setup, cancellation, failure, and cleanup for one run.
// StartContext must return with workers and ports ready. Production runs in
// workers: synchronous sends during setup can block before pipeline wiring.
// The context governs both setup and the run. Failed startup must release
// partial resources before returning. Cancellation requests shutdown; Stop
// must still join all workers and release resources before returning.
// Stop is safe to call concurrently or repeatedly, including before startup.
// A stopped instance may restart; callers must reacquire its channels.
type Lifecycle interface {
	StartContext(context.Context) error
	Stop() error
	FailureReporter
}

// FailureReporter exposes the first terminal error of a run independently of
// the recoverable error channel. Failed must be non-nil after successful startup
// and closes only on failure, not normal Stop. Reacquire Failed after restart.
// Failure remains available until the next run. Accessors must be concurrency-safe
// and nonblocking. Call Stop after failure to join workers and release resources.
type FailureReporter interface {
	Failed() <-chan struct{}
	Failure() error
}

// ManagedPlugin is required by pipelines and registered factories.
// Plugins own their channels; a pipeline borrows them for one run and exclusively
// owns the connected instances' lifecycle. Role must be stable, acquire no
// resources, and match the ports: inputs produce, outputs consume, filters do
// both. Unused ports return nil. ErrorChan reports recoverable diagnostics and
// may be nil; terminal errors use FailureReporter. Base is an optional
// implementation. A convenience Start method is not part of this contract.
type ManagedPlugin interface {
	Lifecycle
	Role() PluginType
	ErrorChan() <-chan error
	InputChan() chan<- event.Event
	OutputChan() <-chan event.Event
}
