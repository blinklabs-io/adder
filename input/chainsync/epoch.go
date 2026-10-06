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
	"github.com/blinklabs-io/adder/internal/config"
	ouroboros "github.com/blinklabs-io/gouroboros"
)

// EraHistory contains the slot boundaries needed to derive an epoch number.
// ShelleyStartSlot is zero for networks that began in Shelley. A zero epoch
// length returns the corresponding era's starting epoch instead of dividing.
type EraHistory struct {
	ByronEpochLength   uint64
	ShelleyStartSlot   uint64
	ShelleyStartEpoch  uint64
	ShelleyEpochLength uint64
}

var knownEraHistory = map[string]EraHistory{
	"mainnet": {
		ByronEpochLength:   21600,
		ShelleyStartSlot:   4492800,
		ShelleyStartEpoch:  208,
		ShelleyEpochLength: 432000,
	},
	"preprod": {
		ByronEpochLength:   21600,
		ShelleyStartSlot:   86400,
		ShelleyStartEpoch:  4,
		ShelleyEpochLength: 432000,
	},
	"preview": {
		ShelleyEpochLength: 86400,
	},
	"sanchonet": {
		ShelleyEpochLength: 86400,
	},
}

// EpochFromSlot returns the epoch containing slot.
func (h EraHistory) EpochFromSlot(slot uint64) uint64 {
	if h.ShelleyStartSlot > 0 && slot < h.ShelleyStartSlot {
		if h.ByronEpochLength == 0 {
			return 0
		}
		return slot / h.ByronEpochLength
	}
	if h.ShelleyEpochLength == 0 {
		return h.ShelleyStartEpoch
	}
	return h.ShelleyStartEpoch +
		(slot-h.ShelleyStartSlot)/h.ShelleyEpochLength
}

// EpochFromSlot derives an epoch using the process-wide genesis configuration.
// ChainSync status uses the instance's configured era history instead.
func EpochFromSlot(slot uint64) uint64 {
	return eraHistoryFromConfig(config.GetConfig()).EpochFromSlot(slot)
}

func (c *ChainSync) resolveEraHistory() {
	if c.eraHistorySet {
		return
	}
	if c.networkMagic != 0 {
		if network, ok := ouroboros.NetworkByNetworkMagic(c.networkMagic); ok {
			if history, ok := knownEraHistory[network.Name]; ok {
				c.eraHistory = history
				return
			}
		}
		c.eraHistory = eraHistoryFromConfig(config.GetConfig())
		return
	}
	if history, ok := knownEraHistory[c.network]; ok {
		c.eraHistory = history
		return
	}
	c.eraHistory = eraHistoryFromConfig(config.GetConfig())
}

func eraHistoryFromConfig(cfg *config.Config) EraHistory {
	history := EraHistory{
		ByronEpochLength:   cfg.ByronGenesis.EpochLength,
		ShelleyEpochLength: cfg.ShelleyGenesis.EpochLength,
	}
	if cfg.ShelleyTransEpoch >= 0 {
		//nolint:gosec // configuration validation rejects negative values
		history.ShelleyStartEpoch = uint64(cfg.ShelleyTransEpoch)
	}
	if cfg.ByronGenesis.EndSlot != nil &&
		(*cfg.ByronGenesis.EndSlot > 0 || history.ShelleyStartEpoch > 0) {
		history.ShelleyStartSlot = *cfg.ByronGenesis.EndSlot + 1
	}
	return history
}
