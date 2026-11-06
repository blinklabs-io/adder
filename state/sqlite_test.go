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

package state

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSqliteStoreGetSetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := NewSqliteStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	_, ok, err := store.Get("missing")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, store.Set("cursor", []byte("first")))
	value, ok, err := store.Get("cursor")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("first"), value)

	// Set overwrites rather than erroring on an existing key.
	require.NoError(t, store.Set("cursor", []byte("second")))
	value, ok, err = store.Get("cursor")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("second"), value)
}

func TestSqliteStorePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")

	store, err := NewSqliteStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Set("cursor", []byte("saved")))
	require.NoError(t, store.Close())

	reopened, err := NewSqliteStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	value, ok, err := reopened.Get("cursor")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("saved"), value)
}
