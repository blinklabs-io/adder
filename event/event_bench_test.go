package event

import (
	"encoding/json"
	"testing"
	"time"
)

var benchmarkEventBytes []byte
var benchmarkDecodedEvent Event

func BenchmarkEventJSON(b *testing.B) {
	evt := Event{
		Type:      TypeTransaction,
		Timestamp: time.Unix(1_700_000_000, 0).UTC(),
		Context: TransactionContext{
			TransactionHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			BlockNumber:     10_000_000,
			SlotNumber:      100_000_000,
			NetworkMagic:    764824073,
		},
		Payload: TransactionEvent{
			BlockHash:       "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			Fee:             180_000,
			Withdrawals:     map[string]uint64{"stake1benchmark": 1_000_000},
			TransactionCbor: []byte{0x83, 0x01, 0x02, 0x03},
		},
	}
	encoded, err := json.Marshal(evt)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			data, err := json.Marshal(evt)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkEventBytes = data
		}
	})
	b.Run("unmarshal", func(b *testing.B) {
		b.SetBytes(int64(len(encoded)))
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			var decoded Event
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				b.Fatal(err)
			}
			benchmarkDecodedEvent = decoded
		}
	})
}
