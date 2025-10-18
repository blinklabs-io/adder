package chainsync

import (
	"encoding/hex"
	//"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SundaeSwap-finance/kugo"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleRollBackward(t *testing.T) {
	// Create a new ChainSync instance
	c := &ChainSync{
		eventChan: make(chan event.Event, 10),
		status:    &ChainSyncStatus{},
	}

	// Define test data
	point := ocommon.Point{
		Slot: 12345,
		Hash: []byte{0x01, 0x02, 0x03, 0x04, 0x05},
	}
	tip := chainsync.Tip{
		Point: ocommon.Point{
			Slot: 67890,
			Hash: []byte{0x06, 0x07, 0x08, 0x09, 0x0A},
		},
	}

	// Call the function under test
	err := c.handleRollBackward(chainsync.CallbackContext{}, point, tip)
	// Verify that no error was returned
	assert.NoError(t, err)

	// Verify that an event was sent to the eventChan
	select {
	case evt := <-c.eventChan:
		// Verify the event type
		assert.Equal(t, "chainsync.rollback", evt.Type)

		// Verify the timestamp is not zero and is close to the current time
		assert.False(t, evt.Timestamp.IsZero())
		assert.WithinDuration(t, time.Now(), evt.Timestamp, time.Second)

		// Verify the payload is of type RollbackEvent and contains the correct data
		assert.IsType(t, event.RollbackEvent{}, evt.Payload)
		rollbackEvent := evt.Payload.(event.RollbackEvent)
		assert.Equal(t, hex.EncodeToString(point.Hash), rollbackEvent.BlockHash)
		assert.Equal(t, point.Slot, rollbackEvent.SlotNumber)

		// Verify the context is nil (since it's not used in handleRollBackward)
		assert.Nil(t, evt.Context)
	case <-time.After(1 * time.Second):
		t.Fatal("Expected event was not sent to eventChan")
	}

	// Verify that the status was updated correctly
	assert.Equal(t, uint64(12345), c.status.SlotNumber)
	assert.Equal(
		t,
		uint64(0),
		c.status.BlockNumber,
	) // BlockNumber should be 0 after rollback
	assert.Equal(t, "0102030405", c.status.BlockHash)
	assert.Equal(t, uint64(67890), c.status.TipSlotNumber)
	assert.Equal(t, "060708090a", c.status.TipBlockHash)
}

func TestGetKupoClient(t *testing.T) {
	// Setup test server
	ts := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	)
	defer ts.Close()

	t.Run("successful client creation", func(t *testing.T) {
		c := &ChainSync{
			kupoUrl: ts.URL,
		}

		client, err := getKupoClient(c)
		require.NoError(t, err)
		assert.NotNil(t, client)
		assert.NotNil(t, c.kupoClient)
	})

	t.Run("returns cached client", func(t *testing.T) {
		mockClient := &kugo.Client{}
		c := &ChainSync{
			kupoUrl:    ts.URL,
			kupoClient: mockClient,
		}

		client, err := getKupoClient(c)
		require.NoError(t, err)
		assert.Same(t, mockClient, client)
	})

	t.Run("health check timeout", func(t *testing.T) {
		slowTS := httptest.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(
					4 * time.Second,
				) // Longer than the 3s context timeout
				w.WriteHeader(http.StatusOK)
			}),
		)
		defer slowTS.Close()

		c := &ChainSync{
			kupoUrl: slowTS.URL,
		}

		_, err := getKupoClient(c)
		require.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"kupo health check timed out after 3 seconds",
		)
	})

	t.Run("failed health check status", func(t *testing.T) {
		failTS := httptest.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}),
		)
		defer failTS.Close()

		c := &ChainSync{
			kupoUrl: failTS.URL,
		}

		_, err := getKupoClient(c)
		require.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"health check failed with status code: 500",
		)
	})

	t.Run("malformed URL", func(t *testing.T) {
		c := &ChainSync{
			kupoUrl: "http://invalid url",
		}

		_, err := getKupoClient(c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid kupo URL")
	})

	t.Run("unreachable host", func(t *testing.T) {
		c := &ChainSync{
			kupoUrl: "http://unreachable-host.invalid",
		}

		_, err := getKupoClient(c)
		require.Error(t, err)
		assert.True(t,
			strings.Contains(err.Error(), "failed to resolve kupo host") ||
				strings.Contains(err.Error(), "failed to perform health check"),
			"unexpected error: %v", err)
	})
}
