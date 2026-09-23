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

package chainsync

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEpochFromSlotByNetwork(t *testing.T) {
	tests := []struct {
		name    string
		options []ChainSyncOptionFunc
		slot    uint64
		want    uint64
	}{
		{
			name:    "mainnet by name",
			options: []ChainSyncOptionFunc{WithNetwork("mainnet")},
			slot:    100000000,
			want:    429,
		},
		{
			name:    "preprod by name",
			options: []ChainSyncOptionFunc{WithNetwork("preprod")},
			slot:    100000000,
			want:    235,
		},
		{
			name:    "preview by name",
			options: []ChainSyncOptionFunc{WithNetwork("preview")},
			slot:    100000000,
			want:    1157,
		},
		{
			name: "network magic overrides network name",
			options: []ChainSyncOptionFunc{
				WithNetwork("mainnet"),
				WithNetworkMagic(1),
			},
			slot: 100000000,
			want: 235,
		},
		{
			name:    "preview boundary",
			options: []ChainSyncOptionFunc{WithNetwork("preview")},
			slot:    86400,
			want:    1,
		},
		{
			name: "custom history overrides known network",
			options: []ChainSyncOptionFunc{
				WithEraHistory(EraHistory{
					ShelleyStartSlot:   100,
					ShelleyStartEpoch:  2,
					ShelleyEpochLength: 10,
				}),
				WithNetwork("mainnet"),
			},
			slot: 150,
			want: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var status ChainSyncStatus
			options := append(
				[]ChainSyncOptionFunc{},
				tt.options...,
			)
			options = append(options, WithStatusUpdateFunc(func(update ChainSyncStatus) {
				status = update
			}))
			c := New(options...)
			c.updateStatus(tt.slot, 0, "", tt.slot, "")
			require.Equal(t, tt.want, c.status.EpochNumber)
			require.Equal(t, tt.want, status.EpochNumber)
		})
	}
}

func TestEpochFromSlotEraBoundaries(t *testing.T) {
	history := knownEraHistory["preprod"]
	require.Equal(t, uint64(3), history.EpochFromSlot(86399))
	require.Equal(t, uint64(4), history.EpochFromSlot(86400))
	require.Equal(t, uint64(5), history.EpochFromSlot(518400))
}
