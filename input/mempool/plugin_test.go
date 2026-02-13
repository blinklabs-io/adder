// Copyright 2025 Blink Labs Software
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

package mempool

import (
	"testing"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/assert"
)

func TestMempoolImplementsPlugin(t *testing.T) {
	var _ plugin.Plugin = (*Mempool)(nil)
}

func TestNewFromCmdlineOptions(t *testing.T) {
	p := NewFromCmdlineOptions()
	assert.NotNil(t, p)
	m, ok := p.(*Mempool)
	assert.True(t, ok)
	assert.NotNil(t, m)
	assert.Nil(t, m.InputChan())
}

func TestNew(t *testing.T) {
	m := New(
		WithNetwork("mainnet"),
		WithSocketPath("/tmp/node.sock"),
	)
	assert.NotNil(t, m)
	assert.Equal(t, "mainnet", m.network)
	assert.Equal(t, "/tmp/node.sock", m.socketPath)
}
