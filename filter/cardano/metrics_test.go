// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package cardano

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

func TestDropObserverCountsOnlyRejectedEvents(t *testing.T) {
	c := New(WithPoolIds([]string{"selected-pool"}))
	var drops atomic.Int64
	c.SetDropObserver(func() { drops.Add(1) })
	require.NoError(t, c.Start())
	t.Cleanup(func() { require.NoError(t, c.Stop()) })

	c.InputChan() <- event.Event{Type: event.TypeBlock, Payload: event.BlockEvent{IssuerVkey: "other-pool"}}
	c.InputChan() <- event.Event{Type: event.TypeRollback}
	select {
	case got := <-c.OutputChan():
		require.Equal(t, event.TypeRollback, got.Type)
	case <-time.After(time.Second):
		t.Fatal("passing event did not cross filter")
	}
	require.EqualValues(t, 1, drops.Load())
}
