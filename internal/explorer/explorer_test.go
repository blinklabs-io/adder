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

package explorer

import "testing"

func TestBaseURL(t *testing.T) {
	cases := map[uint32]string{
		764824073: "https://cexplorer.io",         // mainnet
		1:         "https://preprod.cexplorer.io", // preprod
		2:         "https://preview.cexplorer.io", // preview
		0:         "https://cexplorer.io",         // unset → mainnet
		999:       "https://cexplorer.io",         // unknown → mainnet
	}
	for magic, want := range cases {
		if got := BaseURL(magic); got != want {
			t.Errorf("BaseURL(%d) = %q, want %q", magic, got, want)
		}
	}
}
