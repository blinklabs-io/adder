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

package chainsync

import (
	"encoding/json"
	"testing"

	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/stretchr/testify/assert"
)

func TestResolvedTransactionOutput_MarshalJSON(t *testing.T) {
	// Create the address, handle the error properly
	addr, err := common.NewAddress("addr_test1wq5yehcpw4e3r32rltrww40e6ezdckr9v9l0ehptsxeynlg630pts")
	if err != nil {
		t.Fatalf("Failed to create address: %v", err)
	}

	// Create assets for the resolved output
	assets := common.NewMultiAsset(map[common.Blake2b224]map[cbor.ByteString]uint64{
		common.NewBlake2b224([]byte("policy1")): {cbor.NewByteString([]byte("TokenA")): 100},
	})

	// Create the resolved transaction output
	resolvedOutput := ResolvedTransactionOutput{
		AddressField: addr,
		AmountField:  2000000,
		AssetsField:  &assets,
	}

	// Marshal the resolved transaction output to JSON
	jsonOutput, err := json.Marshal(resolvedOutput)
	if err != nil {
		t.Fatalf("Failed to marshal ResolvedTransactionOutput: %v", err)
	}

	// Expected JSON string
	expectedJSON := `{
      "address":"addr_test1wq5yehcpw4e3r32rltrww40e6ezdckr9v9l0ehptsxeynlg630pts",
      "amount":2000000,
      "assets":[
          {
              "name":"TokenA",
              "nameHex":"546f6b656e41",
              "policyId":"706f6c69637931000000000000000000000000000000000000000000",
              "fingerprint":"asset174ghjk04g2dpjv8zuw6s99rm09wmfvmgtfl84n",
              "amount":100
          }
      ]
  }`

	assert.JSONEq(t, expectedJSON, string(jsonOutput))
}
