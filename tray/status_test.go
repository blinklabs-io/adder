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

package tray

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusTracker_InitialStatus(t *testing.T) {
	tracker := NewStatusTracker()
	assert.Equal(t, StatusStopped, tracker.Status())
}

func TestStatusTracker_SetAndGet(t *testing.T) {
	tracker := NewStatusTracker()

	tracker.Set(StatusConnected)
	assert.Equal(t, StatusConnected, tracker.Status())

	tracker.Set(StatusReconnecting)
	assert.Equal(t, StatusReconnecting, tracker.Status())
}

func TestStatusTracker_ObserverNotified(t *testing.T) {
	tracker := NewStatusTracker()

	var mu sync.Mutex
	var received []Status
	done := make(chan struct{}, 3)
	tracker.OnChange(func(s Status) {
		mu.Lock()
		received = append(received, s)
		mu.Unlock()
		done <- struct{}{}
	})

	tracker.Set(StatusStarting)
	tracker.Set(StatusConnected)

	// Wait for 3 async callbacks (1 from OnChange, 2 from Set)
	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for observer callback")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, received, 3)
	assert.Contains(t, received, StatusStopped)
	assert.Contains(t, received, StatusStarting)
	assert.Contains(t, received, StatusConnected)
}

func TestStatusTracker_ObserverNotCalledForSameStatus(t *testing.T) {
	tracker := NewStatusTracker()

	var callCount atomic.Int32
	done := make(chan struct{}, 3)
	tracker.OnChange(func(s Status) {
		callCount.Add(1)
		done <- struct{}{}
	})

	// Wait for the immediate callback from OnChange
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for immediate observer callback")
	}

	tracker.Set(StatusStarting)
	// Wait for the first Set callback
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for observer callback")
	}

	tracker.Set(StatusStarting) // same status, should not notify

	// Give time for a spurious callback (should not arrive)
	select {
	case <-done:
		t.Fatal("observer called for same status")
	case <-time.After(50 * time.Millisecond):
	}

	assert.Equal(t, int32(2), callCount.Load())
}

func TestStatusTracker_ObserverPanicRecovery(t *testing.T) {
	tracker := NewStatusTracker()

	done := make(chan struct{}, 2)
	tracker.OnChange(func(s Status) {
		defer func() { done <- struct{}{} }()
		panic("test panic")
	})

	// Wait for the immediate panic from OnChange
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for panicking observer (immediate)")
	}

	// Should not panic the caller
	tracker.Set(StatusStarting)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for panicking observer (set)")
	}

	// Tracker still works after observer panic
	tracker.Set(StatusConnected)
	assert.Equal(t, StatusConnected, tracker.Status())
}

func TestStatusTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewStatusTracker()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.Set(StatusConnected)
			_ = tracker.Status()
		}()
	}
	wg.Wait()

	assert.Equal(t, StatusConnected, tracker.Status())
}

func TestStatusTracker_OrderedDelivery(t *testing.T) {
	tracker := NewStatusTracker()

	var mu sync.Mutex
	var got []Status
	const n = 5
	done := make(chan struct{}, n+1)
	tracker.OnChange(func(s Status) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
		done <- struct{}{}
	})

	// A rapid burst of alternating transitions. With per-transition
	// goroutines these could arrive out of order; the single ordered
	// worker must deliver them exactly as sent.
	sent := []Status{
		StatusStarting,
		StatusConnected,
		StatusReconnecting,
		StatusConnected,
		StatusStopped,
	}
	for _, s := range sent {
		tracker.Set(s)
	}

	// n from Set + 1 immediate from OnChange.
	for i := 0; i < n+1; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for ordered delivery")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	// First delivery is the synchronous OnChange call (StatusStopped),
	// followed by the burst in exact send order.
	want := append([]Status{StatusStopped}, sent...)
	assert.Equal(t, want, got)
}

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusStopped, "stopped"},
		{StatusStarting, "starting"},
		{StatusConnected, "connected"},
		{StatusReconnecting, "reconnecting"},
		{StatusError, "error"},
		{Status(99), "unknown"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.status.String())
	}
}
