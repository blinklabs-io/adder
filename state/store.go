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

// Package state provides small persistent key/value storage that plugins
// can use to survive a restart. The motivating case is an input plugin's
// chainsync cursor: without it, a restart re-syncs from genesis or
// whatever intersect point is configured, instead of resuming from where
// it left off.
//
// Values are opaque bytes; callers own their own encoding (JSON, CBOR,
// etc). Keys are chosen by the caller, so a single store can be shared by
// multiple plugins as long as they use distinct keys.
package state

// Store is a minimal persistent key/value store. Implementations must be
// safe for concurrent use.
type Store interface {
	// Get returns the value stored for key. The second return value
	// reports whether a value was found; false with a nil error means
	// no value has ever been saved for key.
	Get(key string) ([]byte, bool, error)

	// Set persists value for key, overwriting any previously stored
	// value.
	Set(key string, value []byte) error

	// Close releases any resources (file handles, database connections)
	// held by the store. The store must not be used afterward.
	Close() error
}
