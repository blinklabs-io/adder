package chainsync

import (
	"encoding/hex"
	"time"

	"testing"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/connection"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/assert"
	utxorpc "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
)

// MockAddr is a mock implementation of the net.Addr interface.
type MockAddr struct {
	NetWorkStr string
	AddrStr    string
}

func (m MockAddr) Network() string {
	return m.NetWorkStr
}

func (m MockAddr) String() string {
	return m.AddrStr
}

// NewMockCallbackContext creates a mock CallbackContext for testing.
func NewMockCallbackContext() chainsync.CallbackContext {
	return chainsync.CallbackContext{
		ConnectionId: connection.ConnectionId{
			LocalAddr:  MockAddr{NetWorkStr: "tcp", AddrStr: "127.0.0.1:3000"},
			RemoteAddr: MockAddr{NetWorkStr: "tcp", AddrStr: "127.0.0.1:4000"},
		},
		Client: nil,
		Server: nil,
	}
}

// MockBlock is a mock implementation of the ledger.Block interface for testing.
type MockBlock struct {
	slotNumber  uint64
	blockNumber uint64
	hash        string
	prevHash    string
	era         ledger.Era
	issuerVkey  ledger.IssuerVkey
	cbor        []byte
}

func (m MockBlock) SlotNumber() uint64 {
	return m.slotNumber
}

func (m MockBlock) BlockNumber() uint64 {
	return m.blockNumber
}

func (m MockBlock) Hash() string {
	return m.hash
}

func (m MockBlock) PrevHash() string {
	return m.prevHash
}

func (m MockBlock) IssuerVkey() ledger.IssuerVkey {
	return m.issuerVkey
}

func (m MockBlock) BlockBodySize() uint64 {
	return 0
}

func (m MockBlock) Era() ledger.Era {
	return m.era
}

func (m MockBlock) Cbor() []byte {
	return m.cbor
}

func (m MockBlock) Header() ledger.BlockHeader {
	return MockBlockHeader{
		slotNumber:    m.slotNumber,
		blockNumber:   m.blockNumber,
		hash:          m.hash,
		prevHash:      m.prevHash,
		era:           m.era,
		issuerVkey:    m.issuerVkey,
		blockBodySize: 0,
		cbor:          m.cbor,
	}
}

func (m MockBlock) Type() int {
	return 0
}

func (m MockBlock) Transactions() []ledger.Transaction {
	return nil
}

func (m MockBlock) Utxorpc() *utxorpc.Block {
	return nil
}

// MockBlockHeader is a mock implementation of the ledger.BlockHeader interface for testing.
type MockBlockHeader struct {
	slotNumber    uint64
	blockNumber   uint64
	hash          string
	prevHash      string
	era           ledger.Era
	issuerVkey    ledger.IssuerVkey
	blockBodySize uint64
	cbor          []byte
}

func (m MockBlockHeader) SlotNumber() uint64 {
	return m.slotNumber
}

func (m MockBlockHeader) BlockNumber() uint64 {
	return m.blockNumber
}

func (m MockBlockHeader) Hash() string {
	return m.hash
}

func (m MockBlockHeader) PrevHash() string {
	return m.prevHash
}

func (m MockBlockHeader) IssuerVkey() ledger.IssuerVkey {
	return m.issuerVkey
}

func (m MockBlockHeader) BlockBodySize() uint64 {
	return m.blockBodySize
}

func (m MockBlockHeader) Era() ledger.Era {
	return m.era
}

func (m MockBlockHeader) Cbor() []byte {
	return m.cbor
}

func TestEraUpdate(t *testing.T) {
	// Create a new ChainSync instance
	chainSync := New()

	// Create a mock block with a specific era
	mockEra := ledger.Era{Name: "MockEra"}
	mockBlock := MockBlock{
		slotNumber:  12345,
		blockNumber: 67890,
		hash:        "mockHash",
		prevHash:    "mockPrevHash",
		era:         mockEra,
		cbor:        []byte("mockCbor"),
	}

	// Create a mock tip
	mockTip := chainsync.Tip{
		Point: ocommon.Point{
			Slot: 12345,
			Hash: []byte("mockTipHash"),
		},
	}

	// Create a mock callback context
	mockCallbackContext := NewMockCallbackContext()

	// Call handleRollForward with the mock block, tip, and callback context
	err := chainSync.handleRollForward(mockCallbackContext, 0, mockBlock, mockTip)
	if err != nil {
		t.Fatalf("handleRollForward failed: %v", err)
	}

	// Check if the era has been correctly updated in the status
	if chainSync.status.Era != mockEra.Name {
		t.Errorf("Expected era to be %s, got %s", mockEra.Name, chainSync.status.Era)
	}

	// Check if other status fields are correctly updated
	if chainSync.status.SlotNumber != mockBlock.SlotNumber() {
		t.Errorf("Expected slot number to be %d, got %d", mockBlock.SlotNumber(), chainSync.status.SlotNumber)
	}

	if chainSync.status.BlockNumber != mockBlock.BlockNumber() {
		t.Errorf("Expected block number to be %d, got %d", mockBlock.BlockNumber(), chainSync.status.BlockNumber)
	}

	if chainSync.status.BlockHash != mockBlock.Hash() {
		t.Errorf("Expected block hash to be %s, got %s", mockBlock.Hash(), chainSync.status.BlockHash)
	}

	if chainSync.status.TipSlotNumber != mockTip.Point.Slot {
		t.Errorf("Expected tip slot number to be %d, got %d", mockTip.Point.Slot, chainSync.status.TipSlotNumber)
	}

	if chainSync.status.TipBlockHash != hex.EncodeToString(mockTip.Point.Hash) {
		t.Errorf("Expected tip block hash to be %s, got %s", hex.EncodeToString(mockTip.Point.Hash), chainSync.status.TipBlockHash)
	}
}

func TestUpdateStatusWithEra(t *testing.T) {
	// Create a mock ChainSync object
	chainSync := &ChainSync{
		status: &ChainSyncStatus{},
		statusUpdateFunc: func(status ChainSyncStatus) {
			assert.Equal(t, "TestEra", status.Era, "Expected the Era field to be 'TestEra'")
		},
	}

	// Call updateStatus with mock data including an era
	chainSync.updateStatus(
		100,        // SlotNumber
		1,          // BlockNumber
		"testHash", // BlockHash
		100,        // TipSlotNumber
		"tipHash",  // TipBlockHash
		"TestEra",  // Era
	)

	assert.Equal(t, uint64(100), chainSync.status.SlotNumber, "Expected SlotNumber to be 100")
	assert.Equal(t, uint64(1), chainSync.status.BlockNumber, "Expected BlockNumber to be 1")
	assert.Equal(t, "testHash", chainSync.status.BlockHash, "Expected BlockHash to be 'testHash'")
	assert.Equal(t, uint64(100), chainSync.status.TipSlotNumber, "Expected TipSlotNumber to be 100")
	assert.Equal(t, "tipHash", chainSync.status.TipBlockHash, "Expected TipBlockHash to be 'tipHash'")
	assert.Equal(t, "TestEra", chainSync.status.Era, "Expected Era to be 'TestEra'")
}

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
