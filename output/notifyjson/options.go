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

package notifyjson

import (
	"io"
	"time"
)

type Option func(*Output)

func WithConfigPath(path string) Option {
	return func(o *Output) { o.configPath = path }
}

func WithWriter(w io.Writer) Option {
	return func(o *Output) {
		if w != nil {
			o.writer = w
		}
	}
}

func WithStaleAfter(d time.Duration) Option {
	return func(o *Output) { o.staleAfterOverride = d }
}
