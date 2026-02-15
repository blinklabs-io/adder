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

	"github.com/blinklabs-io/gouroboros/cbor"
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

// GovActionIdData represents a reference to a previous governance action
type GovActionIdData struct {
	TransactionId string `json:"transactionId"`
	GovActionIdx  uint32 `json:"govActionIdx"`
}

// GovActionData contains the action-specific data (typed union - only one field populated)
type GovActionData struct {
	ParameterChange    *ParameterChangeActionData    `json:"parameterChange,omitempty"`
	HardForkInitiation *HardForkInitiationActionData `json:"hardForkInitiation,omitempty"`
	TreasuryWithdrawal *TreasuryWithdrawalActionData `json:"treasuryWithdrawal,omitempty"`
	NoConfidence       *NoConfidenceActionData       `json:"noConfidence,omitempty"`
	UpdateCommittee    *UpdateCommitteeActionData    `json:"updateCommittee,omitempty"`
	NewConstitution    *NewConstitutionActionData    `json:"newConstitution,omitempty"`
	Info               *InfoActionData               `json:"info,omitempty"`
}

// InfoActionData represents an Info governance action (no payload)
type InfoActionData struct{}

// NoConfidenceActionData represents a No Confidence governance action
type NoConfidenceActionData struct {
	PrevActionId *GovActionIdData `json:"prevActionId,omitempty"`
}

// HardForkInitiationActionData represents a Hard Fork Initiation governance action
type HardForkInitiationActionData struct {
	PrevActionId    *GovActionIdData `json:"prevActionId,omitempty"`
	ProtocolVersion ProtocolVersion  `json:"protocolVersion"`
}

// ProtocolVersion represents a protocol version (major.minor)
type ProtocolVersion struct {
	Major uint `json:"major"`
	Minor uint `json:"minor"`
}

// TreasuryWithdrawalActionData represents a Treasury Withdrawal governance action
type TreasuryWithdrawalActionData struct {
	Withdrawals []TreasuryWithdrawalItem `json:"withdrawals"`
	PolicyHash  string                   `json:"policyHash,omitempty"`
}

// TreasuryWithdrawalItem represents a single treasury withdrawal destination
type TreasuryWithdrawalItem struct {
	Address string `json:"address"`
	Amount  uint64 `json:"amount"`
}

// UpdateCommitteeActionData represents an Update Committee governance action
type UpdateCommitteeActionData struct {
	PrevActionId      *GovActionIdData  `json:"prevActionId,omitempty"`
	MembersToRemove   []string          `json:"membersToRemove"`
	MembersToAdd      []CommitteeMember `json:"membersToAdd"`
	QuorumNumerator   uint64            `json:"quorumNumerator"`
	QuorumDenominator uint64            `json:"quorumDenominator"`
}

// CommitteeMember represents a committee member with their term epoch
type CommitteeMember struct {
	Credential string `json:"credential"`
	Epoch      uint   `json:"epoch"`
}

// NewConstitutionActionData represents a New Constitution governance action
type NewConstitutionActionData struct {
	PrevActionId *GovActionIdData `json:"prevActionId,omitempty"`
	Anchor       AnchorData       `json:"anchor"`
	ScriptHash   string           `json:"scriptHash,omitempty"`
}

// ParameterChangeActionData represents a Parameter Change governance action
type ParameterChangeActionData struct {
	PrevActionId *GovActionIdData             `json:"prevActionId,omitempty"`
	PolicyHash   string                       `json:"policyHash,omitempty"`
	ParamUpdate  *ProtocolParameterUpdateData `json:"paramUpdate,omitempty"`
}

// ProtocolParameterUpdateData contains protocol parameter changes
type ProtocolParameterUpdateData struct {
	// Fee parameters
	MinFeeA *uint `json:"minFeeA,omitempty"`
	MinFeeB *uint `json:"minFeeB,omitempty"`

	// Block/tx limits
	MaxBlockBodySize   *uint `json:"maxBlockBodySize,omitempty"`
	MaxTxSize          *uint `json:"maxTxSize,omitempty"`
	MaxBlockHeaderSize *uint `json:"maxBlockHeaderSize,omitempty"`

	// Deposits
	KeyDeposit  *uint `json:"keyDeposit,omitempty"`
	PoolDeposit *uint `json:"poolDeposit,omitempty"`

	// Pool parameters
	MaxEpoch    *uint    `json:"maxEpoch,omitempty"`
	NOpt        *uint    `json:"nOpt,omitempty"`
	A0          *float64 `json:"a0,omitempty"`
	Rho         *float64 `json:"rho,omitempty"`
	Tau         *float64 `json:"tau,omitempty"`
	MinPoolCost *uint64  `json:"minPoolCost,omitempty"`

	// Plutus parameters
	AdaPerUtxoByte       *uint64         `json:"adaPerUtxoByte,omitempty"`
	MaxValueSize         *uint           `json:"maxValueSize,omitempty"`
	CollateralPercentage *uint           `json:"collateralPercentage,omitempty"`
	MaxCollateralInputs  *uint           `json:"maxCollateralInputs,omitempty"`
	MaxTxExUnits         *ExUnitsData    `json:"maxTxExUnits,omitempty"`
	MaxBlockExUnits      *ExUnitsData    `json:"maxBlockExUnits,omitempty"`
	ExecutionCosts       *ExecutionCosts `json:"executionCosts,omitempty"`

	// Governance parameters (Conway)
	MinCommitteeSize           *uint    `json:"minCommitteeSize,omitempty"`
	CommitteeTermLimit         *uint64  `json:"committeeTermLimit,omitempty"`
	GovActionValidityPeriod    *uint64  `json:"govActionValidityPeriod,omitempty"`
	GovActionDeposit           *uint64  `json:"govActionDeposit,omitempty"`
	DRepDeposit                *uint64  `json:"drepDeposit,omitempty"`
	DRepInactivityPeriod       *uint64  `json:"drepInactivityPeriod,omitempty"`
	MinFeeRefScriptCostPerByte *float64 `json:"minFeeRefScriptCostPerByte,omitempty"`

	// Voting thresholds
	PoolVotingThresholds *PoolVotingThresholdsData `json:"poolVotingThresholds,omitempty"`
	DRepVotingThresholds *DRepVotingThresholdsData `json:"drepVotingThresholds,omitempty"`
}

// ExUnitsData represents execution units (memory and steps)
type ExUnitsData struct {
	Mem   int64 `json:"mem"`
	Steps int64 `json:"steps"`
}

// ExecutionCosts represents the price of execution units
type ExecutionCosts struct {
	MemPrice  *float64 `json:"memPrice,omitempty"`
	StepPrice *float64 `json:"stepPrice,omitempty"`
}

// PoolVotingThresholdsData represents SPO voting thresholds
type PoolVotingThresholdsData struct {
	MotionNoConfidence *float64 `json:"motionNoConfidence,omitempty"`
	CommitteeNormal    *float64 `json:"committeeNormal,omitempty"`
	CommitteeNoConf    *float64 `json:"committeeNoConf,omitempty"`
	HardForkInitiation *float64 `json:"hardForkInitiation,omitempty"`
	SecurityGroup      *float64 `json:"securityGroup,omitempty"`
}

// DRepVotingThresholdsData represents DRep voting thresholds
type DRepVotingThresholdsData struct {
	MotionNoConfidence *float64 `json:"motionNoConfidence,omitempty"`
	CommitteeNormal    *float64 `json:"committeeNormal,omitempty"`
	CommitteeNoConf    *float64 `json:"committeeNoConf,omitempty"`
	UpdateConstitution *float64 `json:"updateConstitution,omitempty"`
	HardForkInitiation *float64 `json:"hardForkInitiation,omitempty"`
	NetworkGroup       *float64 `json:"networkGroup,omitempty"`
	EconomicGroup      *float64 `json:"economicGroup,omitempty"`
	TechnicalGroup     *float64 `json:"technicalGroup,omitempty"`
	GovernanceGroup    *float64 `json:"governanceGroup,omitempty"`
	TreasuryWithdrawal *float64 `json:"treasuryWithdrawal,omitempty"`
}

// ProposalProcedureData represents a governance proposal
type ProposalProcedureData struct {
	Index         uint32        `json:"index"`
	Deposit       uint64        `json:"deposit"`
	RewardAccount string        `json:"rewardAccount"`
	ActionType    string        `json:"actionType"`
	ActionData    GovActionData `json:"actionData"`
	Anchor        AnchorData    `json:"anchor,omitempty"`
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
			ActionData:    extractGovActionData(prop.GovAction()),
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
		if data, ok := extractDRepCertificate(cert); ok {
			drep = append(drep, data)
			continue
		}
		switch c := cert.(type) {
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
	return drep, voteDel, committee
}

// ExtractDRepCertificates extracts DRep certificates from a transaction
func ExtractDRepCertificates(tx ledger.Transaction) []DRepCertificateData {
	if len(tx.Certificates()) == 0 {
		return nil
	}

	var result []DRepCertificateData
	for _, cert := range tx.Certificates() {
		if data, ok := extractDRepCertificate(cert); ok {
			result = append(result, data)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func drepBech32Prefix(cred lcommon.Credential) string {
	if cred.CredType == 1 {
		return "drep_script"
	}
	return "drep"
}

func extractDRepCertificate(cert ledger.Certificate) (DRepCertificateData, bool) {
	switch c := cert.(type) {
	case *lcommon.RegistrationDrepCertificate:
		data := DRepCertificateData{
			CertificateType: DRepCertificateTypeRegistration,
			DRepHash:        hex.EncodeToString(c.DrepCredential.Hash().Bytes()),
			DRepId:          c.DrepCredential.Hash().Bech32(drepBech32Prefix(c.DrepCredential)),
			Deposit:         c.Amount,
		}
		// Certificate anchors are pointer types (*GovAnchor), so we check for nil
		if c.Anchor != nil {
			data.Anchor = AnchorData{
				Url:      c.Anchor.Url,
				DataHash: hex.EncodeToString(c.Anchor.DataHash[:]),
			}
		}
		return data, true

	case *lcommon.DeregistrationDrepCertificate:
		return DRepCertificateData{
			CertificateType: DRepCertificateTypeDeregistration,
			DRepHash:        hex.EncodeToString(c.DrepCredential.Hash().Bytes()),
			DRepId:          c.DrepCredential.Hash().Bech32(drepBech32Prefix(c.DrepCredential)),
			Deposit:         c.Amount,
		}, true

	case *lcommon.UpdateDrepCertificate:
		data := DRepCertificateData{
			CertificateType: DRepCertificateTypeUpdate,
			DRepHash:        hex.EncodeToString(c.DrepCredential.Hash().Bytes()),
			DRepId:          c.DrepCredential.Hash().Bech32(drepBech32Prefix(c.DrepCredential)),
		}
		if c.Anchor != nil {
			data.Anchor = AnchorData{
				Url:      c.Anchor.Url,
				DataHash: hex.EncodeToString(c.Anchor.DataHash[:]),
			}
		}
		return data, true
	}

	return DRepCertificateData{}, false
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

// extractGovActionId converts a gouroboros GovActionId to our JSON-friendly struct
func extractGovActionId(id *lcommon.GovActionId) *GovActionIdData {
	if id == nil {
		return nil
	}
	return &GovActionIdData{
		TransactionId: hex.EncodeToString(id.TransactionId[:]),
		GovActionIdx:  id.GovActionIdx,
	}
}

// extractGovActionData extracts action-specific data from a GovAction
func extractGovActionData(action lcommon.GovAction) GovActionData {
	var data GovActionData

	switch a := action.(type) {
	case *lcommon.InfoGovAction:
		data.Info = &InfoActionData{}

	case *lcommon.NoConfidenceGovAction:
		data.NoConfidence = &NoConfidenceActionData{
			PrevActionId: extractGovActionId(a.ActionId),
		}

	case *lcommon.HardForkInitiationGovAction:
		data.HardForkInitiation = &HardForkInitiationActionData{
			PrevActionId: extractGovActionId(a.ActionId),
			ProtocolVersion: ProtocolVersion{
				Major: a.ProtocolVersion.Major,
				Minor: a.ProtocolVersion.Minor,
			},
		}

	case *lcommon.TreasuryWithdrawalGovAction:
		data.TreasuryWithdrawal = extractTreasuryWithdrawalAction(a)

	case *lcommon.UpdateCommitteeGovAction:
		data.UpdateCommittee = extractUpdateCommitteeAction(a)

	case *lcommon.NewConstitutionGovAction:
		data.NewConstitution = extractNewConstitutionAction(a)

	case *conway.ConwayParameterChangeGovAction:
		data.ParameterChange = extractParameterChangeAction(a)
	}

	return data
}

func extractTreasuryWithdrawalAction(a *lcommon.TreasuryWithdrawalGovAction) *TreasuryWithdrawalActionData {
	data := &TreasuryWithdrawalActionData{
		Withdrawals: make([]TreasuryWithdrawalItem, 0, len(a.Withdrawals)),
	}
	if len(a.PolicyHash) > 0 {
		data.PolicyHash = hex.EncodeToString(a.PolicyHash)
	}
	for addr, amount := range a.Withdrawals {
		data.Withdrawals = append(data.Withdrawals, TreasuryWithdrawalItem{
			Address: addr.String(),
			Amount:  amount,
		})
	}
	return data
}

func extractUpdateCommitteeAction(a *lcommon.UpdateCommitteeGovAction) *UpdateCommitteeActionData {
	data := &UpdateCommitteeActionData{
		PrevActionId:    extractGovActionId(a.ActionId),
		MembersToRemove: make([]string, 0, len(a.Credentials)),
		MembersToAdd:    make([]CommitteeMember, 0, len(a.CredEpochs)),
	}

	// Extract quorum as numerator/denominator
	if a.Quorum.Num() != nil {
		data.QuorumNumerator = a.Quorum.Num().Uint64()
	}
	if a.Quorum.Denom() != nil {
		data.QuorumDenominator = a.Quorum.Denom().Uint64()
	}

	// Members to remove
	for _, cred := range a.Credentials {
		data.MembersToRemove = append(data.MembersToRemove, hex.EncodeToString(cred.Hash().Bytes()))
	}

	// Members to add with term epochs
	for cred, epoch := range a.CredEpochs {
		data.MembersToAdd = append(data.MembersToAdd, CommitteeMember{
			Credential: hex.EncodeToString(cred.Hash().Bytes()),
			Epoch:      epoch,
		})
	}

	return data
}

func extractNewConstitutionAction(a *lcommon.NewConstitutionGovAction) *NewConstitutionActionData {
	data := &NewConstitutionActionData{
		PrevActionId: extractGovActionId(a.ActionId),
		Anchor: AnchorData{
			Url:      a.Constitution.Anchor.Url,
			DataHash: hex.EncodeToString(a.Constitution.Anchor.DataHash[:]),
		},
	}
	if len(a.Constitution.ScriptHash) > 0 {
		data.ScriptHash = hex.EncodeToString(a.Constitution.ScriptHash)
	}
	return data
}

func extractParameterChangeAction(a *conway.ConwayParameterChangeGovAction) *ParameterChangeActionData {
	data := &ParameterChangeActionData{
		PrevActionId: extractGovActionId(a.ActionId),
	}
	if len(a.PolicyHash) > 0 {
		data.PolicyHash = hex.EncodeToString(a.PolicyHash)
	}
	data.ParamUpdate = extractProtocolParameterUpdate(&a.ParamUpdate)
	return data
}

func extractProtocolParameterUpdate(u *conway.ConwayProtocolParameterUpdate) *ProtocolParameterUpdateData {
	data := &ProtocolParameterUpdateData{
		MinFeeA:                    u.MinFeeA,
		MinFeeB:                    u.MinFeeB,
		MaxBlockBodySize:           u.MaxBlockBodySize,
		MaxTxSize:                  u.MaxTxSize,
		MaxBlockHeaderSize:         u.MaxBlockHeaderSize,
		KeyDeposit:                 u.KeyDeposit,
		PoolDeposit:                u.PoolDeposit,
		MaxEpoch:                   u.MaxEpoch,
		NOpt:                       u.NOpt,
		A0:                         rationalToFloat(u.A0),
		Rho:                        rationalToFloat(u.Rho),
		Tau:                        rationalToFloat(u.Tau),
		MinPoolCost:                u.MinPoolCost,
		AdaPerUtxoByte:             u.AdaPerUtxoByte,
		MaxValueSize:               u.MaxValueSize,
		CollateralPercentage:       u.CollateralPercentage,
		MaxCollateralInputs:        u.MaxCollateralInputs,
		MinCommitteeSize:           u.MinCommitteeSize,
		CommitteeTermLimit:         u.CommitteeTermLimit,
		GovActionValidityPeriod:    u.GovActionValidityPeriod,
		GovActionDeposit:           u.GovActionDeposit,
		DRepDeposit:                u.DRepDeposit,
		DRepInactivityPeriod:       u.DRepInactivityPeriod,
		MinFeeRefScriptCostPerByte: rationalToFloat(u.MinFeeRefScriptCostPerByte),
	}

	// ExUnits
	if u.MaxTxExUnits != nil {
		data.MaxTxExUnits = &ExUnitsData{
			Mem:   u.MaxTxExUnits.Memory,
			Steps: u.MaxTxExUnits.Steps,
		}
	}
	if u.MaxBlockExUnits != nil {
		data.MaxBlockExUnits = &ExUnitsData{
			Mem:   u.MaxBlockExUnits.Memory,
			Steps: u.MaxBlockExUnits.Steps,
		}
	}

	// Execution costs
	if u.ExecutionCosts != nil {
		data.ExecutionCosts = &ExecutionCosts{
			MemPrice:  rationalToFloat(u.ExecutionCosts.MemPrice),
			StepPrice: rationalToFloat(u.ExecutionCosts.StepPrice),
		}
	}

	// Voting thresholds
	if u.PoolVotingThresholds != nil {
		data.PoolVotingThresholds = extractPoolVotingThresholds(u.PoolVotingThresholds)
	}
	if u.DRepVotingThresholds != nil {
		data.DRepVotingThresholds = extractDRepVotingThresholds(u.DRepVotingThresholds)
	}

	return data
}

func extractPoolVotingThresholds(t *conway.PoolVotingThresholds) *PoolVotingThresholdsData {
	return &PoolVotingThresholdsData{
		MotionNoConfidence: rationalToFloat(&t.MotionNoConfidence),
		CommitteeNormal:    rationalToFloat(&t.CommitteeNormal),
		CommitteeNoConf:    rationalToFloat(&t.CommitteeNoConfidence),
		HardForkInitiation: rationalToFloat(&t.HardForkInitiation),
		SecurityGroup:      rationalToFloat(&t.PpSecurityGroup),
	}
}

func extractDRepVotingThresholds(t *conway.DRepVotingThresholds) *DRepVotingThresholdsData {
	return &DRepVotingThresholdsData{
		MotionNoConfidence: rationalToFloat(&t.MotionNoConfidence),
		CommitteeNormal:    rationalToFloat(&t.CommitteeNormal),
		CommitteeNoConf:    rationalToFloat(&t.CommitteeNoConfidence),
		UpdateConstitution: rationalToFloat(&t.UpdateToConstitution),
		HardForkInitiation: rationalToFloat(&t.HardForkInitiation),
		NetworkGroup:       rationalToFloat(&t.PpNetworkGroup),
		EconomicGroup:      rationalToFloat(&t.PpEconomicGroup),
		TechnicalGroup:     rationalToFloat(&t.PpTechnicalGroup),
		GovernanceGroup:    rationalToFloat(&t.PpGovGroup),
		TreasuryWithdrawal: rationalToFloat(&t.TreasuryWithdrawal),
	}
}

func rationalToFloat(r *cbor.Rat) *float64 {
	if r == nil {
		return nil
	}
	num := r.Num()
	denom := r.Denom()
	if num == nil || denom == nil || denom.Uint64() == 0 {
		return nil
	}
	result := float64(num.Int64()) / float64(denom.Uint64())
	return &result
}
