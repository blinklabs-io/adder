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

import (
	"encoding/hex"
	"math"

	"github.com/blinklabs-io/gouroboros/ledger"
	lcommon "github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/blinklabs-io/gouroboros/ledger/conway"
)

// GovernanceContext provides the context for governance events
type GovernanceContext struct {
	TransactionHash string `json:"transactionHash"`
	BlockNumber     uint64 `json:"blockNumber"`
	SlotNumber      uint64 `json:"slotNumber"`
	TransactionIdx  uint32 `json:"transactionIdx"`
	NetworkMagic    uint32 `json:"networkMagic"`
}

// GovernanceEvent contains all governance-related data from a transaction
type GovernanceEvent struct {
	Transaction     ledger.Transaction `json:"-"`
	BlockHash       string             `json:"blockHash"`
	TransactionCbor byteSliceJsonHex   `json:"transactionCbor,omitempty"`

	// Proposal procedures (governance actions created in this tx)
	ProposalProcedures []ProposalProcedureData `json:"proposalProcedures,omitempty"`

	// Voting procedures (votes cast in this tx)
	VotingProcedures []VotingProcedureData `json:"votingProcedures,omitempty"`

	// DRep certificates (registrations, updates, retirements)
	DRepCertificates []DRepCertificateData `json:"drepCertificates,omitempty"`

	// Vote delegation certificates
	VoteDelegationCertificates []VoteDelegationCertificateData `json:"voteDelegationCertificates,omitempty"`

	// Constitutional Committee certificates
	CommitteeCertificates []CommitteeCertificateData `json:"committeeCertificates,omitempty"`
}

// ProposalProcedureData represents a governance proposal
type ProposalProcedureData struct {
	Index         uint32     `json:"index"`
	Deposit       uint64     `json:"deposit"`
	RewardAccount string     `json:"rewardAccount"`
	ActionType    string     `json:"actionType"`
	Anchor        AnchorData `json:"anchor,omitempty"`
}

// VotingProcedureData represents a vote cast
type VotingProcedureData struct {
	VoterType      string     `json:"voterType"`
	VoterHash      string     `json:"voterHash"`
	VoterId        string     `json:"voterId"`
	GovActionTxId  string     `json:"govActionTxId"`
	GovActionIndex uint32     `json:"govActionIndex"`
	Vote           string     `json:"vote"`
	Anchor         AnchorData `json:"anchor,omitempty"`
}

// DRepCertificateData represents DRep registration/update/retirement
type DRepCertificateData struct {
	CertificateType string     `json:"certificateType"`
	DRepHash        string     `json:"drepHash"`
	DRepId          string     `json:"drepId"`
	Deposit         int64      `json:"deposit,omitempty"`
	Anchor          AnchorData `json:"anchor,omitempty"`
}

// VoteDelegationCertificateData represents vote delegation
type VoteDelegationCertificateData struct {
	CertificateType string `json:"certificateType"`
	StakeCredential string `json:"stakeCredential"`
	DRepType        string `json:"drepType"`
	DRepHash        string `json:"drepHash,omitempty"`
	DRepId          string `json:"drepId,omitempty"`
	PoolKeyHash     string `json:"poolKeyHash,omitempty"`
	Deposit         int64  `json:"deposit,omitempty"`
}

// CommitteeCertificateData represents CC hot key auth or resignation
type CommitteeCertificateData struct {
	CertificateType string     `json:"certificateType"`
	ColdCredential  string     `json:"coldCredential"`
	HotCredential   string     `json:"hotCredential,omitempty"`
	Anchor          AnchorData `json:"anchor,omitempty"`
}

// AnchorData represents a governance anchor (URL + hash)
type AnchorData struct {
	Url      string `json:"url"`
	DataHash string `json:"dataHash"`
}

// NewGovernanceContext creates a new GovernanceContext
func NewGovernanceContext(
	block ledger.Block,
	tx ledger.Transaction,
	index uint32,
	networkMagic uint32,
) GovernanceContext {
	return GovernanceContext{
		BlockNumber:     block.BlockNumber(),
		SlotNumber:      block.SlotNumber(),
		TransactionHash: tx.Hash().String(),
		TransactionIdx:  index,
		NetworkMagic:    networkMagic,
	}
}

// NewGovernanceEvent creates a new GovernanceEvent from a transaction
func NewGovernanceEvent(
	block ledger.Block,
	tx ledger.Transaction,
	includeCbor bool,
) GovernanceEvent {
	evt := GovernanceEvent{
		Transaction: tx,
		BlockHash:   block.Hash().String(),
	}
	if includeCbor {
		evt.TransactionCbor = tx.Cbor()
	}

	// Extract proposal procedures
	evt.ProposalProcedures = extractProposalProcedures(tx)

	// Extract voting procedures
	evt.VotingProcedures = extractVotingProcedures(tx)

	// Extract governance certificates
	evt.DRepCertificates, evt.VoteDelegationCertificates, evt.CommitteeCertificates = extractGovernanceCertificates(tx)

	return evt
}

// HasGovernanceData returns true if the transaction contains any governance data
func HasGovernanceData(tx ledger.Transaction) bool {
	if len(tx.VotingProcedures()) > 0 {
		return true
	}
	if len(tx.ProposalProcedures()) > 0 {
		return true
	}
	for _, cert := range tx.Certificates() {
		if isGovernanceCertificate(cert) {
			return true
		}
	}
	return false
}

func isGovernanceCertificate(cert ledger.Certificate) bool {
	switch cert.(type) {
	case *lcommon.RegistrationDrepCertificate,
		*lcommon.DeregistrationDrepCertificate,
		*lcommon.UpdateDrepCertificate,
		*lcommon.VoteDelegationCertificate,
		*lcommon.StakeVoteDelegationCertificate,
		*lcommon.VoteRegistrationDelegationCertificate,
		*lcommon.StakeVoteRegistrationDelegationCertificate,
		*lcommon.AuthCommitteeHotCertificate,
		*lcommon.ResignCommitteeColdCertificate:
		return true
	}
	return false
}

func extractProposalProcedures(tx ledger.Transaction) []ProposalProcedureData {
	proposals := tx.ProposalProcedures()
	if len(proposals) == 0 {
		return nil
	}
	// Cardano protocol limits proposals per transaction; this check guards against
	// hypothetical future changes that could cause index overflow
	if len(proposals) > math.MaxUint32 {
		return nil
	}

	result := make([]ProposalProcedureData, 0, len(proposals))
	for i, prop := range proposals {
		data := ProposalProcedureData{
			Index:         uint32(i), //nolint:gosec // bounds checked above
			Deposit:       prop.Deposit(),
			RewardAccount: prop.RewardAccount().String(),
			ActionType:    getGovActionType(prop.GovAction()),
		}
		// prop.Anchor() returns a value type (GovAnchor, not *GovAnchor),
		// so we check for empty URL to determine if anchor data is present
		if anchor := prop.Anchor(); anchor.Url != "" {
			data.Anchor = AnchorData{
				Url:      anchor.Url,
				DataHash: hex.EncodeToString(anchor.DataHash[:]),
			}
		}
		result = append(result, data)
	}
	return result
}

func extractVotingProcedures(tx ledger.Transaction) []VotingProcedureData {
	procedures := tx.VotingProcedures()
	if len(procedures) == 0 {
		return nil
	}

	var result []VotingProcedureData
	for voter, actions := range procedures {
		for actionId, procedure := range actions {
			data := VotingProcedureData{
				VoterType:      getVoterType(voter.Type),
				VoterHash:      hex.EncodeToString(voter.Hash[:]),
				VoterId:        voter.String(),
				GovActionTxId:  hex.EncodeToString(actionId.TransactionId[:]),
				GovActionIndex: actionId.GovActionIdx,
				Vote:           getVoteString(procedure.Vote),
			}
			// procedure.Anchor is a pointer type (*GovAnchor), so we check for nil
			if procedure.Anchor != nil {
				data.Anchor = AnchorData{
					Url:      procedure.Anchor.Url,
					DataHash: hex.EncodeToString(procedure.Anchor.DataHash[:]),
				}
			}
			result = append(result, data)
		}
	}
	return result
}

func extractGovernanceCertificates(tx ledger.Transaction) (
	drep []DRepCertificateData,
	voteDel []VoteDelegationCertificateData,
	committee []CommitteeCertificateData,
) {
	for _, cert := range tx.Certificates() {
		switch c := cert.(type) {
		// DRep certificates
		case *lcommon.RegistrationDrepCertificate:
			data := DRepCertificateData{
				CertificateType: "Registration",
				DRepHash:        hex.EncodeToString(c.DrepCredential.Hash().Bytes()),
				DRepId:          c.DrepCredential.Hash().Bech32("drep"),
				Deposit:         c.Amount,
			}
			// Certificate anchors are pointer types (*GovAnchor), so we check for nil
			if c.Anchor != nil {
				data.Anchor = AnchorData{
					Url:      c.Anchor.Url,
					DataHash: hex.EncodeToString(c.Anchor.DataHash[:]),
				}
			}
			drep = append(drep, data)

		case *lcommon.DeregistrationDrepCertificate:
			drep = append(drep, DRepCertificateData{
				CertificateType: "Deregistration",
				DRepHash:        hex.EncodeToString(c.DrepCredential.Hash().Bytes()),
				DRepId:          c.DrepCredential.Hash().Bech32("drep"),
				Deposit:         c.Amount,
			})

		case *lcommon.UpdateDrepCertificate:
			data := DRepCertificateData{
				CertificateType: "Update",
				DRepHash:        hex.EncodeToString(c.DrepCredential.Hash().Bytes()),
				DRepId:          c.DrepCredential.Hash().Bech32("drep"),
			}
			if c.Anchor != nil {
				data.Anchor = AnchorData{
					Url:      c.Anchor.Url,
					DataHash: hex.EncodeToString(c.Anchor.DataHash[:]),
				}
			}
			drep = append(drep, data)

		// Vote delegation certificates
		case *lcommon.VoteDelegationCertificate:
			voteDel = append(voteDel, extractVoteDelegation("VoteDelegation", c.StakeCredential, c.Drep, 0))

		case *lcommon.StakeVoteDelegationCertificate:
			data := extractVoteDelegation("StakeVoteDelegation", c.StakeCredential, c.Drep, 0)
			data.PoolKeyHash = hex.EncodeToString(c.PoolKeyHash.Bytes())
			voteDel = append(voteDel, data)

		case *lcommon.VoteRegistrationDelegationCertificate:
			voteDel = append(voteDel, extractVoteDelegation("VoteRegistrationDelegation", c.StakeCredential, c.Drep, c.Amount))

		case *lcommon.StakeVoteRegistrationDelegationCertificate:
			data := extractVoteDelegation("StakeVoteRegistrationDelegation", c.StakeCredential, c.Drep, c.Amount)
			data.PoolKeyHash = hex.EncodeToString(c.PoolKeyHash.Bytes())
			voteDel = append(voteDel, data)

		// Constitutional Committee certificates
		case *lcommon.AuthCommitteeHotCertificate:
			committee = append(committee, CommitteeCertificateData{
				CertificateType: "AuthHot",
				ColdCredential:  hex.EncodeToString(c.ColdCredential.Hash().Bytes()),
				HotCredential:   hex.EncodeToString(c.HotCredential.Hash().Bytes()),
			})

		case *lcommon.ResignCommitteeColdCertificate:
			data := CommitteeCertificateData{
				CertificateType: "ResignCold",
				ColdCredential:  hex.EncodeToString(c.ColdCredential.Hash().Bytes()),
			}
			if c.Anchor != nil {
				data.Anchor = AnchorData{
					Url:      c.Anchor.Url,
					DataHash: hex.EncodeToString(c.Anchor.DataHash[:]),
				}
			}
			committee = append(committee, data)
		}
	}
	return
}

// Helper functions

func getGovActionType(action lcommon.GovAction) string {
	switch action.(type) {
	case *conway.ConwayParameterChangeGovAction:
		return "ParameterChange"
	case *lcommon.HardForkInitiationGovAction:
		return "HardForkInitiation"
	case *lcommon.TreasuryWithdrawalGovAction:
		return "TreasuryWithdrawal"
	case *lcommon.NoConfidenceGovAction:
		return "NoConfidence"
	case *lcommon.UpdateCommitteeGovAction:
		return "UpdateCommittee"
	case *lcommon.NewConstitutionGovAction:
		return "NewConstitution"
	case *lcommon.InfoGovAction:
		return "Info"
	default:
		return "Unknown"
	}
}

func getVoterType(vType uint8) string {
	switch vType {
	case lcommon.VoterTypeConstitutionalCommitteeHotKeyHash,
		lcommon.VoterTypeConstitutionalCommitteeHotScriptHash:
		return "CCHot"
	case lcommon.VoterTypeDRepKeyHash, lcommon.VoterTypeDRepScriptHash:
		return "DRep"
	case lcommon.VoterTypeStakingPoolKeyHash:
		return "SPO"
	default:
		return "Unknown"
	}
}

func getVoteString(vote uint8) string {
	switch vote {
	case lcommon.GovVoteNo:
		return "No"
	case lcommon.GovVoteYes:
		return "Yes"
	case lcommon.GovVoteAbstain:
		return "Abstain"
	default:
		return "Unknown"
	}
}

func extractVoteDelegation(
	certType string,
	stakeCred lcommon.Credential,
	drep lcommon.Drep,
	deposit int64,
) VoteDelegationCertificateData {
	data := VoteDelegationCertificateData{
		CertificateType: certType,
		StakeCredential: hex.EncodeToString(stakeCred.Hash().Bytes()),
		Deposit:         deposit,
	}

	switch drep.Type {
	case lcommon.DrepTypeAddrKeyHash:
		data.DRepType = "KeyHash"
		data.DRepHash = hex.EncodeToString(drep.Credential)
		data.DRepId = lcommon.NewBlake2b224(drep.Credential).Bech32("drep")
	case lcommon.DrepTypeScriptHash:
		data.DRepType = "ScriptHash"
		data.DRepHash = hex.EncodeToString(drep.Credential)
		data.DRepId = lcommon.NewBlake2b224(drep.Credential).Bech32("drep_script")
	case lcommon.DrepTypeAbstain:
		data.DRepType = "Abstain"
	case lcommon.DrepTypeNoConfidence:
		data.DRepType = "NoConfidence"
	}

	return data
}
