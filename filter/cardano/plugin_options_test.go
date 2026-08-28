// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cardano

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// withCmdlineOptions sets the package-level options for the duration of a
// test and restores the previous values afterwards
func withCmdlineOptions(t *testing.T, apply func()) {
	t.Helper()
	orig := cmdlineOptions
	t.Cleanup(func() { cmdlineOptions = orig })
	cmdlineOptions = struct {
		address  string
		asset    string
		policyId string
		poolId   string
		drepId   string
	}{}
	apply()
}

func TestNewFromCmdlineOptionsTrimsAddresses(t *testing.T) {
	withCmdlineOptions(t, func() {
		// As produced by a YAML folded block scalar (>-)
		cmdlineOptions.address = "addr1aaa, addr1bbb, stake1ccc"
	})
	c, ok := NewFromCmdlineOptions().(*Cardano)
	assert.True(t, ok, "plugin should be a *Cardano")
	assert.True(t, c.filterSet.hasAddressFilter)
	assert.Equal(
		t,
		map[string]struct{}{
			"addr1aaa": {},
			"addr1bbb": {},
		},
		c.filterSet.addresses.paymentAddresses,
	)
	assert.Equal(
		t,
		map[string]struct{}{"stake1ccc": {}},
		c.filterSet.addresses.stakeAddresses,
	)
}

func TestNewFromCmdlineOptionsTrimsAssets(t *testing.T) {
	withCmdlineOptions(t, func() {
		cmdlineOptions.asset = "asset1aaa,\n asset1bbb"
	})
	c, ok := NewFromCmdlineOptions().(*Cardano)
	assert.True(t, ok, "plugin should be a *Cardano")
	assert.True(t, c.filterSet.hasAssetFilter)
	assert.Equal(
		t,
		map[string]struct{}{
			"asset1aaa": {},
			"asset1bbb": {},
		},
		c.filterSet.assets.fingerprints,
	)
}

func TestNewFromCmdlineOptionsTrimsPolicies(t *testing.T) {
	withCmdlineOptions(t, func() {
		cmdlineOptions.policyId = "policy1, policy2"
	})
	c, ok := NewFromCmdlineOptions().(*Cardano)
	assert.True(t, ok, "plugin should be a *Cardano")
	assert.True(t, c.filterSet.hasPolicyFilter)
	assert.Equal(
		t,
		map[string]struct{}{
			"policy1": {},
			"policy2": {},
		},
		c.filterSet.policies.policyIds,
	)
}

func TestNewFromCmdlineOptionsTrimsPoolIds(t *testing.T) {
	const (
		poolA = "00000000000000000000000000000000000000000000000000000001"
		poolB = "00000000000000000000000000000000000000000000000000000002"
	)
	withCmdlineOptions(t, func() {
		cmdlineOptions.poolId = poolA + ", " + poolB
	})
	c, ok := NewFromCmdlineOptions().(*Cardano)
	assert.True(t, ok, "plugin should be a *Cardano")
	assert.True(t, c.filterSet.hasPoolFilter)
	assert.Equal(
		t,
		map[string]struct{}{
			poolA: {},
			poolB: {},
		},
		c.filterSet.pools.hexPoolIds,
	)
}

func TestNewFromCmdlineOptionsTrimsDRepIds(t *testing.T) {
	const drepId = "00000000000000000000000000000000000000000000000000000003"
	withCmdlineOptions(t, func() {
		// Mixed case plus whitespace: both must be normalized
		cmdlineOptions.drepId = " " + drepId + ",\n ABCDEF" +
			"00000000000000000000000000000000000000000000000004"
	})
	c, ok := NewFromCmdlineOptions().(*Cardano)
	assert.True(t, ok, "plugin should be a *Cardano")
	assert.True(t, c.filterSet.hasDRepFilter)
	assert.Equal(
		t,
		map[string]struct{}{
			drepId: {},
			"abcdef00000000000000000000000000000000000000000000000004": {},
		},
		c.filterSet.dreps.hexDRepIds,
	)
}

// A single address with leading whitespace must still match, as reported in
// blinklabs-io/adder#815
func TestNewFromCmdlineOptionsTrimsSingleAddress(t *testing.T) {
	withCmdlineOptions(t, func() {
		cmdlineOptions.address = " addr1aaa"
	})
	c, ok := NewFromCmdlineOptions().(*Cardano)
	assert.True(t, ok, "plugin should be a *Cardano")
	_, exists := c.filterSet.addresses.paymentAddresses["addr1aaa"]
	assert.True(t, exists, "address should be stored without leading space")
}
