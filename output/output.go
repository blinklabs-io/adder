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

package output

// We import the various plugins that we want to be auto-registered
import (
	_ "github.com/blinklabs-io/snek/output/log"
	_ "github.com/blinklabs-io/snek/output/notify"
	_ "github.com/blinklabs-io/snek/output/push"
	_ "github.com/blinklabs-io/snek/output/webhook"
)
