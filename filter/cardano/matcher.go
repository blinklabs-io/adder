// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package cardano

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/blinklabs-io/adder/event"
	eventfilter "github.com/blinklabs-io/adder/filter/event"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/btcsuite/btcd/btcutil/bech32"
)

// Criteria selects Cardano identities within one event family. Different
// criterion kinds combine with AND, except pool and DRep identities combine
// with OR, matching the legacy Cardano filter.
type Criteria struct {
	Addresses         []string `json:"addresses,omitempty"`
	Policies          []string `json:"policies,omitempty"`
	AssetFingerprints []string `json:"asset_fingerprints,omitempty"`
	PoolIDs           []string `json:"pool_ids,omitempty"`
	DRepIDs           []string `json:"drep_ids,omitempty"`
}

// Matcher is an immutable, compiled rule for one canonical event family.
// Its Match method is safe to call concurrently.
type Matcher struct {
	eventType string
	filters   filterSet
}

// Compile validates the event family and criteria, and copies their lookup
// state. Changes to criteria after compilation do not change the matcher.
func Compile(eventType string, criteria Criteria) (*Matcher, error) {
	if _, err := eventfilter.Compile([]string{eventType}); err != nil {
		return nil, err
	}
	if err := validateCriteria(eventType, criteria); err != nil {
		return nil, err
	}
	c := New()
	if len(criteria.Addresses) > 0 {
		WithAddresses(criteria.Addresses)(c)
	}
	if len(criteria.Policies) > 0 {
		WithPolicies(criteria.Policies)(c)
	}
	if len(criteria.AssetFingerprints) > 0 {
		WithAssetFingerprints(criteria.AssetFingerprints)(c)
	}
	if len(criteria.PoolIDs) > 0 {
		WithPoolIds(criteria.PoolIDs)(c)
	}
	if len(criteria.DRepIDs) > 0 {
		WithDRepIds(criteria.DRepIDs)(c)
	}
	return &Matcher{eventType: eventType, filters: c.filterSet}, nil
}

// Match rejects other event families and mismatched payload types before
// applying the same read-only predicates used by the legacy filter plugin.
func (m *Matcher) Match(evt event.Event) bool {
	if m == nil || evt.Type != m.eventType {
		return false
	}
	switch evt.Type {
	case event.TypeBlock:
		payload, ok := evt.Payload.(event.BlockEvent)
		return ok && m.filters.filterBlockEvent(payload)
	case event.TypeTransaction:
		payload, ok := evt.Payload.(event.TransactionEvent)
		return ok && m.filters.filterTransactionEvent(payload)
	case event.TypeGovernance:
		payload, ok := evt.Payload.(event.GovernanceEvent)
		return ok && m.filters.filterGovernanceEvent(payload)
	case event.TypeDRepRegistration, event.TypeDRepUpdate, event.TypeDRepRetirement:
		payload, ok := evt.Payload.(event.DRepCertificateEvent)
		return ok && m.filters.filterDRepCertificateEvent(payload)
	case event.TypeRollback:
		_, ok := evt.Payload.(event.RollbackEvent)
		return ok
	default:
		return false
	}
}

func validateCriteria(eventType string, c Criteria) error {
	transaction := eventType == event.TypeTransaction
	governance := eventType == event.TypeGovernance
	drep := eventType == event.TypeDRepRegistration ||
		eventType == event.TypeDRepUpdate || eventType == event.TypeDRepRetirement
	checks := []struct {
		name    string
		values  []string
		allowed bool
		valid   func(string) bool
	}{
		{"address", c.Addresses, transaction || governance, validAddress},
		{"policy", c.Policies, transaction, validPolicy},
		{"asset", c.AssetFingerprints, transaction, validAsset},
		{"pool", c.PoolIDs, transaction || governance || eventType == event.TypeBlock, validPool},
		{"drep", c.DRepIDs, transaction || governance || drep, validDRep},
	}
	for _, check := range checks {
		if len(check.values) > 0 && !check.allowed {
			return fmt.Errorf("%s criterion is incompatible with event family", check.name)
		}
		for _, value := range check.values {
			if !check.valid(value) {
				return fmt.Errorf("invalid %s criterion", check.name)
			}
		}
	}
	return nil
}

func validAddress(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	_, err := ledger.NewAddress(value)
	return err == nil
}

func validPolicy(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 28 && value == strings.ToLower(value)
}

func validAsset(value string) bool {
	data, err := decodeID(value, "asset")
	return err == nil && len(data) == 20
}

func validPool(value string) bool {
	if validPolicy(value) {
		return true
	}
	data, err := decodeID(value, "pool")
	return err == nil && len(data) == 28
}

func validDRep(value string) bool {
	if validPolicy(value) {
		return true
	}
	data, err := decodeID(value, "drep", "drep_script")
	return err == nil && (len(data) == 28 || len(data) == 29)
}

func decodeID(value string, prefixes ...string) ([]byte, error) {
	if value != strings.ToLower(value) {
		return nil, errors.New("identifier must be lowercase")
	}
	hrp, data, err := bech32.Decode(value)
	if err != nil {
		return nil, err
	}
	for _, prefix := range prefixes {
		if hrp == prefix {
			return bech32.ConvertBits(data, 5, 8, false)
		}
	}
	return nil, errors.New("unexpected identifier prefix")
}
