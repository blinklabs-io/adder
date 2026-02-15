// Copyright 2025 Blink Labs Software
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

package event

import "github.com/blinklabs-io/gouroboros/ledger"

const (
	DRepCertificateTypeRegistration   = "Registration"
	DRepCertificateTypeUpdate         = "Update"
	DRepCertificateTypeDeregistration = "Deregistration"
)

const (
	DRepRegistrationEventType   = "chainsync.drep.registration"
	DRepUpdateEventType         = "chainsync.drep.update"
	DRepDeregistrationEventType = "chainsync.drep.deregistration"
)

// DRepCertificateEvent represents a single DRep certificate event
type DRepCertificateEvent struct {
	BlockHash   string              `json:"blockHash"`
	Certificate DRepCertificateData `json:"certificate"`
}

// NewDRepCertificateEvent creates a new DRepCertificateEvent
func NewDRepCertificateEvent(
	block ledger.Block,
	cert DRepCertificateData,
) DRepCertificateEvent {
	return DRepCertificateEvent{
		BlockHash:   block.Hash().String(),
		Certificate: cert,
	}
}

// DRepEventType returns the event type for the given certificate type
func DRepEventType(certType string) (string, bool) {
	switch certType {
	case DRepCertificateTypeRegistration:
		return DRepRegistrationEventType, true
	case DRepCertificateTypeUpdate:
		return DRepUpdateEventType, true
	case DRepCertificateTypeDeregistration:
		return DRepDeregistrationEventType, true
	default:
		return "", false
	}
}
