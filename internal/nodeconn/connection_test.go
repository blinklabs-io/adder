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

package nodeconn

import (
	"net"
	"reflect"
	"testing"

	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/stretchr/testify/require"
)

func TestAppendConnectionOptionDoesNotMutateCallerCapacity(t *testing.T) {
	original := ouroboros.WithNetworkMagic(42)
	sentinel := ouroboros.WithNetworkMagic(43)
	options := make([]ouroboros.ConnectionOptionFunc, 1, 2)
	options[0] = original
	backing := options[:2]
	backing[1] = sentinel
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })

	result := appendConnectionOption(options, left)

	require.Len(t, result, 2)
	require.Len(t, options, 1)
	require.Equal(
		t,
		reflect.ValueOf(sentinel).Pointer(),
		reflect.ValueOf(backing[1]).Pointer(),
	)
}
