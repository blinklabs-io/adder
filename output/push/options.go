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

package push

import "github.com/blinklabs-io/adder/plugin"

type PushOptionFunc func(*PushOutput)

// WithLogger specifies the logger object to use for logging messages
func WithLogger(logger plugin.Logger) PushOptionFunc {
	return func(o *PushOutput) {
		o.logger = logger
	}
}

func WithServiceAccountFilePath(serviceAccountFilePath string) PushOptionFunc {
	return func(o *PushOutput) {
		o.serviceAccountFilePath = serviceAccountFilePath
	}
}

func WithAccessTokenUrl(url string) PushOptionFunc {
	return func(o *PushOutput) {
		o.accessTokenUrl = url
	}
}
