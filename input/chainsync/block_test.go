package chainsync

import (
	"testing"

	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/stretchr/testify/assert"
	utxorpc "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
)

// MockIssuerVkey to implement IssuerVkey interface
type MockIssuerVkey struct{}

func (m MockIssuerVkey) Bytes() []byte {
	return []byte{0x01, 0x02, 0x03}
}

func (m MockIssuerVkey) Hash() []byte {
	return []byte{0x04, 0x05, 0x06}
}

// MockBlockHeader implements BlockHeader interface
type MockBlockHeader struct {
	hash          string
	prevHash      string
	blockNumber   uint64
	slotNumber    uint64
	issuerVkey    common.IssuerVkey
	blockBodySize uint64
	era           common.Era
	cborBytes     []byte
}

func (m MockBlockHeader) Hash() string {
	return m.hash
}

func (m MockBlockHeader) PrevHash() string {
	return m.prevHash
}

func (m MockBlockHeader) BlockNumber() uint64 {
	return m.blockNumber
}

func (m MockBlockHeader) SlotNumber() uint64 {
	return m.slotNumber
}

func (m MockBlockHeader) IssuerVkey() common.IssuerVkey {
	return m.issuerVkey
}

func (m MockBlockHeader) BlockBodySize() uint64 {
	return m.blockBodySize
}

func (m MockBlockHeader) Era() common.Era {
	return m.era
}

func (m MockBlockHeader) Cbor() []byte {
	return m.cborBytes
}

// MockBlock implements Block interface
type MockBlock struct {
	MockBlockHeader
	transactions []common.Transaction
}

func (m MockBlock) Header() common.BlockHeader {
	return m.MockBlockHeader
}

func (m MockBlock) Type() int {
	return 0
}

func (m MockBlock) Transactions() []common.Transaction {
	return m.transactions
}

func (m MockBlock) Utxorpc() *utxorpc.Block {
	return nil
}

func (m MockBlock) IsShelley() bool {
	return m.era.Name == "Shelley"
}

func (m MockBlock) IsAllegra() bool {
	return m.era.Name == "Allegra"
}

func (m MockBlock) IsMary() bool {
	return m.era.Name == "Mary"
}

func (m MockBlock) IsAlonzo() bool {
	return m.era.Name == "Alonzo"
}

func (m MockBlock) IsBabbage() bool {
	return m.era.Name == "Babbage"
}

func (m MockBlock) IsConway() bool {
	return m.era.Name == "Conway"
}

func TestNewBlockContext(t *testing.T) {
	testCases := []struct {
		name          string
		block         MockBlock
		networkMagic  uint32
		expectedEra   string
		expectedBlock uint64
		expectedSlot  uint64
	}{
		{
			name: "Shelley Era Block",
			block: MockBlock{
				MockBlockHeader: MockBlockHeader{
					blockNumber: 1000,
					slotNumber:  5000,
					era: common.Era{
						Name: "Shelley",
					},
					hash:          "sample-hash-shelley",
					prevHash:      "prev-hash-shelley",
					blockBodySize: 1024,
					cborBytes:     []byte{0x01, 0x02, 0x03},
				},
				transactions: nil,
			},
			networkMagic:  764824073,
			expectedEra:   "Shelley",
			expectedBlock: 1000,
			expectedSlot:  5000,
		},
		{
			name: "Allegra Era Block",
			block: MockBlock{
				MockBlockHeader: MockBlockHeader{
					blockNumber: 2500,
					slotNumber:  10000,
					era: common.Era{
						Name: "Allegra",
					},
					hash:          "another-hash-allegra",
					prevHash:      "prev-hash-allegra",
					blockBodySize: 2048,
					cborBytes:     []byte{0x04, 0x05, 0x06},
				},
				transactions: nil,
			},
			networkMagic:  1097911063,
			expectedEra:   "Allegra",
			expectedBlock: 2500,
			expectedSlot:  10000,
		},
		{
			name: "Mary Era Block",
			block: MockBlock{
				MockBlockHeader: MockBlockHeader{
					blockNumber: 5000,
					slotNumber:  25000,
					era: common.Era{
						Name: "Mary",
					},
					hash:          "mary-block-hash",
					prevHash:      "prev-hash-mary",
					blockBodySize: 4096,
					cborBytes:     []byte{0x07, 0x08, 0x09},
				},
				transactions: nil,
			},
			networkMagic:  0,
			expectedEra:   "Mary",
			expectedBlock: 5000,
			expectedSlot:  25000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			blockContext := NewBlockContext(tc.block, tc.networkMagic)
			assert.Equal(
				t,
				tc.expectedEra,
				blockContext.Era,
				"Era should match",
			)
			assert.Equal(
				t,
				tc.expectedBlock,
				blockContext.BlockNumber,
				"Block number should match",
			)
			assert.Equal(
				t,
				tc.expectedSlot,
				blockContext.SlotNumber,
				"Slot number should match",
			)
			assert.Equal(
				t,
				tc.networkMagic,
				blockContext.NetworkMagic,
				"Network magic should match",
			)
		})
	}
}

func TestNewBlockContextEdgeCases(t *testing.T) {
	testCases := []struct {
		name         string
		block        MockBlock
		networkMagic uint32
		expectedEra  string
	}{
		{
			name: "Zero Values",
			block: MockBlock{
				MockBlockHeader: MockBlockHeader{
					blockNumber: 0,
					slotNumber:  0,
					era: common.Era{
						Name: "",
					},
					hash:          "",
					prevHash:      "",
					blockBodySize: 0,
					cborBytes:     []byte{},
				},
				transactions: nil,
			},
			networkMagic: 0,
			expectedEra:  "",
		},
		{
			name: "Very Large Numbers",
			block: MockBlock{
				MockBlockHeader: MockBlockHeader{
					blockNumber: ^uint64(0), // Max uint64 value
					slotNumber:  ^uint64(0),
					era: common.Era{
						Name: "Alonzo",
					},
					hash:          "max-block-hash",
					prevHash:      "max-prev-hash",
					blockBodySize: ^uint64(0),
					cborBytes:     []byte{0x0A, 0x0B, 0x0C},
				},
				transactions: nil,
			},
			networkMagic: ^uint32(0), // Max uint32 value
			expectedEra:  "Alonzo",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			blockContext := NewBlockContext(tc.block, tc.networkMagic)
			assert.Equal(
				t,
				tc.expectedEra,
				blockContext.Era,
				"Era should match",
			)
			assert.Equal(
				t,
				tc.block.BlockNumber(),
				blockContext.BlockNumber,
				"Block number should match",
			)
			assert.Equal(
				t,
				tc.block.SlotNumber(),
				blockContext.SlotNumber,
				"Slot number should match",
			)
			assert.Equal(
				t,
				tc.networkMagic,
				blockContext.NetworkMagic,
				"Network magic should match",
			)
		})
	}
}
