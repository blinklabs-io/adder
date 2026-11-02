package cardano

import (
	"bytes"
	"testing"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	mockledger "github.com/blinklabs-io/ouroboros-mock/ledger"
)

var benchmarkFilterResult bool

func BenchmarkCardanoFilter(b *testing.B) {
	const address = "addr_test1qz2fxv2umyhttkxyxp8x0dlpdt3k6cwng5pxj3jhsydzer3jcu5d8ps7zex2k2xt3uqxgjqnnj83ws8lhrn648jjxtwq2ytjqp"
	policy := common.Blake2b224Hash([]byte("benchmark-policy"))
	assetName := []byte("benchmark-asset")
	output, err := mockledger.NewTransactionOutputBuilder().
		WithAddress(address).
		WithLovelace(1_000_000).
		WithAssets(mockledger.Asset{PolicyId: policy[:], AssetName: assetName, Amount: 1}).
		Build()
	if err != nil {
		b.Fatal(err)
	}
	tx := event.Event{
		Type: event.TypeTransaction,
		Payload: event.TransactionEvent{
			Outputs:        []ledger.TransactionOutput{output},
			ResolvedInputs: []ledger.TransactionOutput{output},
		},
	}
	poolID := bytes.Repeat([]byte{0x42}, 28)
	poolHex := common.Blake2b224Hash(poolID).String()
	block := event.Event{
		Type: event.TypeBlock,
		Payload: event.BlockEvent{
			IssuerVkey: poolHex,
		},
	}

	for _, tc := range []struct {
		name      string
		filter    *Cardano
		evt       event.Event
		wantMatch bool
	}{
		{name: "address_match", filter: New(WithAddresses([]string{address})), evt: tx, wantMatch: true},
		{name: "address_miss", filter: New(WithAddresses([]string{"addr_test1other"})), evt: tx},
		{name: "asset_match", filter: New(WithAssetFingerprints([]string{ledger.NewAssetFingerprint(policy[:], assetName).String()})), evt: tx, wantMatch: true},
		{name: "asset_miss", filter: New(WithAssetFingerprints([]string{"asset1other"})), evt: tx},
		{name: "pool_match", filter: New(WithPoolIds([]string{poolHex})), evt: block, wantMatch: true},
		{name: "pool_miss", filter: New(WithPoolIds([]string{common.Blake2b224Hash([]byte("other-pool")).String()})), evt: block},
	} {
		b.Run(tc.name, func(b *testing.B) {
			if got := tc.filter.filterEvent(tc.evt); got != tc.wantMatch {
				b.Fatalf("filter result = %t, want %t", got, tc.wantMatch)
			}
			b.ReportAllocs()
			b.ResetTimer()
			var matched bool
			for range b.N {
				matched = tc.filter.filterEvent(tc.evt)
			}
			benchmarkFilterResult = matched
		})
	}
}
