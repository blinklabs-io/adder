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

package embedded

import "github.com/blinklabs-io/adder/event"

type EmbeddedOptionFunc func(*EmbeddedOutput)

// WithCallbackFunc specifies a callback function for events
func WithCallbackFunc(callbackFunc CallbackFunc) EmbeddedOptionFunc {
	return func(o *EmbeddedOutput) {
		o.callbackFunc = callbackFunc
	}
}

// WithOutputChan specifies an event.Event channel to use for events
func WithOutputChan(outputChan chan event.Event) EmbeddedOptionFunc {
	return func(o *EmbeddedOutput) {
		o.outputChan = outputChan
	}
}
