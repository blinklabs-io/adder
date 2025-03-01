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
	assert.Equal(t, uint64(0), c.status.BlockNumber) // BlockNumber should be 0 after rollback
	assert.Equal(t, "0102030405", c.status.BlockHash)
	assert.Equal(t, uint64(67890), c.status.TipSlotNumber)
	assert.Equal(t, "060708090a", c.status.TipBlockHash)
}

func TestUpdateStatusWithEra(t *testing.T) {
	chainSync := &ChainSync{
		status: &ChainSyncStatus{},
	}

	testCases := []struct {
		slotNumber  uint64
		expectedEra string
		expectedTip bool
	}{
		{0, "Byron", false},          // lower boundary case
		{4492799, "Byron", false},    // edge of Byron
		{4492800, "Shelley", false},  // start of Shelley
		{15983999, "Shelley", false}, // edge of Shelley
		{15984000, "Allegra", false}, // start of Allegra
		{34559999, "Allegra", false}, // edge of Allegra
		{34560000, "Mary", false},    // start of Mary
		{38879999, "Mary", false},    // edge of Mary
		{38880000, "Alonzo", false},  // start of Alonzo
		{43199999, "Alonzo", false},  // edge of Alonzo
		{43200000, "Babbage", false}, // start of Babbage
		{50399999, "Babbage", false}, // edge of Babbage
		{50400000, "Conway", false},  // start of Conway
		{99999999, "Conway", false},  // future slot case
	}

	for _, tc := range testCases {
		blockHash := "abcdef1234567890"
		blockHashBytes, _ := hex.DecodeString(blockHash)

		chainSync.updateStatus(tc.slotNumber, 100, blockHash, 60000000, "hash")
		assert.Equal(t, tc.expectedEra, chainSync.status.Era, "Unexpected era for slot %d", tc.slotNumber)
		assert.Equal(t, tc.slotNumber, chainSync.status.SlotNumber)
		assert.Equal(t, blockHash, chainSync.status.BlockHash)
		assert.Contains(t, chainSync.cursorCache, ocommon.Point{Slot: tc.slotNumber, Hash: blockHashBytes})
	}

	// Additional edge case: Testing tipReached logic
	chainSync.bulkRangeEnd.Slot = 50000000 // example bulk range end
	chainSync.updateStatus(60000001, 101, "abcd1234", 60000001, "hash")
	assert.True(t, chainSync.status.TipReached, "TipReached should be true when slotNumber >= tipSlotNumber")
}

func TestGetEra(t *testing.T) {
	c := &ChainSync{}

	tests := []struct {
		slotNumber uint64
		expected   string
	}{
		{0, "Byron"},           // start of Byron
		{4492799, "Byron"},     // end of Byron
		{4492800, "Shelley"},   // start of Shelley
		{15983999, "Shelley"},  // end of Shelley
		{15984000, "Allegra"},  // start of Allegra
		{34559999, "Allegra"},  // end of Allegra
		{34560000, "Mary"},     // start of Mary
		{38879999, "Mary"},     // end of Mary
		{38880000, "Alonzo"},   // start of Alonzo
		{43199999, "Alonzo"},   // end of Alonzo
		{43200000, "Babbage"},  // start of Babbage
		{50399999, "Babbage"},  // end of Babbage
		{50400000, "Conway"},   // start of Conway
		{9999999999, "Conway"}, // far future but still Conway
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := c.getEra(tt.slotNumber)
			if result != tt.expected {
				t.Errorf("getEra(%d) = %s; want %s", tt.slotNumber, result, tt.expected)
			}
		})
	}
}
