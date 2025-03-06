package chainsync

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/assert"
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
		assert.IsType(t, RollbackEvent{}, evt.Payload)
		rollbackEvent := evt.Payload.(RollbackEvent)
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
