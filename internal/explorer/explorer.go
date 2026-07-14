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

// Package explorer maps a Cardano network magic to the block-explorer base URL
// used when building links in notifications (telegram, webhook, and the tray).
// Keeping this in one place means an explorer or domain change is a single edit.
package explorer

// Cardano network magics. These are stable protocol constants; mainnet
// (764824073) and any unrecognized magic fall through to the mainnet explorer.
const (
	preprodMagic uint32 = 1
	previewMagic uint32 = 2
)

// BaseURL returns the cexplorer.io base URL for the given network magic:
// mainnet at the apex, each testnet on its own subdomain.
func BaseURL(networkMagic uint32) string {
	switch networkMagic {
	case preprodMagic:
		return "https://preprod.cexplorer.io"
	case previewMagic:
		return "https://preview.cexplorer.io"
	default:
		return "https://cexplorer.io"
	}
}
