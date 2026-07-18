# Governance Events

Adder emits structured events for on-chain governance actions and certificate activity on the Cardano blockchain. These events capture Conway-era governance data, allowing you to monitor proposal procedures, voting activity, DRep registrations, vote delegations, and Constitutional Committee changes.

## Overview

The chainsync input plugin emits an `input.governance` event for every block transaction that contains Conway-era on-chain governance data. A single transaction produces exactly one `input.governance` event, and that event collects all of the governance data found in that transaction.

Governance events are emitted *in addition to* the regular `input.transaction` events for the same transaction. Transactions with no governance data do not produce an `input.governance` event.

- **Event Type Name**: `input.governance`

---

## Event Structure

A governance event consists of a top-level JSON object containing a `timestamp`, `type`, `context`, and `payload`.

### Context Fields

The `context` object identifies the transaction and chain position where the governance data was found.

| Field | Type | Description |
| :--- | :--- | :--- |
| `transactionHash` | string | The 32-byte hex-encoded hash of the transaction containing the governance data. |
| `blockNumber` | number | The block height (absolute number) containing the transaction. |
| `slotNumber` | number | The slot number of the containing block. |
| `transactionIdx` | number | The index of the transaction within the block (0-based). |
| `networkMagic` | number | The network magic identifier of the connected node. |

### Payload Fields

The `payload` object contains block information and arrays of governance-related items. Each array is omitted when empty.

| Field | Type | Description |
| :--- | :--- | :--- |
| `blockHash` | string | The hex-encoded block hash containing the transaction. |
| `transactionCbor` | string | Raw transaction CBOR in hex form (present only when `--input-chainsync-include-cbor` is set). |
| `proposalProcedures` | array | List of governance actions proposed in this transaction. |
| `votingProcedures` | array | List of votes cast in this transaction. |
| `drepCertificates` | array | List of DRep registration, update, or retirement certificates. |
| `voteDelegationCertificates` | array | List of stake-credential to DRep vote-delegation certificates. |
| `committeeCertificates` | array | List of Constitutional Committee authorization or resignation certificates. |

---

## Embedded Data Structures

### `proposalProcedures[]`

Represents a proposed governance action (new proposal).

| Field | Type | Description |
| :--- | :--- | :--- |
| `index` | number | The index of the proposal within the transaction's proposals. |
| `deposit` | number | The deposit amount in Lovelaces locked for this proposal. |
| `rewardAccount` | string | The stake/reward address where the deposit will be returned upon completion. |
| `actionType` | string | One of: `ParameterChange`, `HardForkInitiation`, `TreasuryWithdrawal`, `NoConfidence`, `UpdateCommittee`, `NewConstitution`, `Info`. |
| `actionData` | object | Action-specific data; exactly one field is populated, keyed by the action (e.g. `parameterChange`, `treasuryWithdrawal`, `newConstitution`). |
| `anchor` | object | Optional `{ "url", "dataHash" }` referencing off-chain metadata. |

### `votingProcedures[]`

Represents a vote cast on an active proposal.

| Field | Type | Description |
| :--- | :--- | :--- |
| `voterType` | string | One of: `DRep` (Delegate Representative), `SPO` (Stake Pool Operator), `CCHot` (Constitutional Committee Hot Credential). |
| `voterHash` | string | Hex-encoded credential hash of the voter. |
| `voterId` | string | Bech32 identifier of the voter (e.g., `drep1...`). |
| `govActionTxId` | string | Transaction ID of the governance action being voted on. |
| `govActionIndex` | number | Index of the governance action within that transaction. |
| `vote` | string | The vote cast; one of `Yes`, `No`, or `Abstain`. |
| `anchor` | object | Optional `{ "url", "dataHash" }` referencing off-chain vote rationale metadata. |

### `drepCertificates[]`

Represents changes to a DRep's status.

| Field | Type | Description |
| :--- | :--- | :--- |
| `certificateType` | string | One of `Registration`, `Update`, `Deregistration`. |
| `drepHash` | string | Hex-encoded DRep credential hash. |
| `drepId` | string | Bech32 DRep ID (`drep1...` or `drep_script1...`). |
| `deposit` | number | Lovelace deposit required for registration or refunded on deregistration. |
| `anchor` | object | Optional `{ "url", "dataHash" }` referencing off-chain DRep metadata. |

### `voteDelegationCertificates[]`

Represents a stake address delegating its voting power to a DRep or a pre-defined state.

| Field | Type | Description |
| :--- | :--- | :--- |
| `certificateType` | string | One of: `VoteDelegation`, `StakeVoteDelegation`, `VoteRegistrationDelegation`, `StakeVoteRegistrationDelegation`. |
| `stakeCredential` | string | Hex-encoded stake credential hash of the delegator. |
| `drepType` | string | The target of the delegation; one of `KeyHash`, `ScriptHash`, `Abstain`, `NoConfidence`. |
| `drepHash` | string | Hex-encoded DRep credential hash (omitted for `Abstain`/`NoConfidence`). |
| `drepId` | string | Bech32 DRep ID (omitted for `Abstain`/`NoConfidence`). |
| `poolKeyHash` | string | Hex-encoded pool key hash (present only for combined stake and vote delegation types). |
| `deposit` | number | Lovelace deposit amount (present only for registration-delegation types). |

### `committeeCertificates[]`

Represents Constitutional Committee credential changes.

| Field | Type | Description |
| :--- | :--- | :--- |
| `certificateType` | string | `AuthHot` (authorizing hot credential) or `ResignCold` (resigning cold credential). |
| `coldCredential` | string | Hex-encoded committee cold credential hash. |
| `hotCredential` | string | Hex-encoded committee hot credential hash (present only for `AuthHot`). |
| `anchor` | object | Optional `{ "url", "dataHash" }` metadata (present only for `ResignCold`). |

---

## Supported Governance Actions

There are seven distinct proposal action types in the Conway ledger:

1. **`ParameterChange`**: Proposes an update to one or more network protocol parameters.
2. **`HardForkInitiation`**: Proposes a hard fork upgrade to a newer protocol version.
3. **`TreasuryWithdrawal`**: Proposes withdrawing Lovelace funds from the treasury to specified reward accounts.
4. **`NoConfidence`**: Proposes a state of no confidence in the current Constitutional Committee.
5. **`UpdateCommittee`**: Proposes a change to the Constitutional Committee membership, threshold, or terms.
6. **`NewConstitution`**: Proposes an update to the network Constitution and its off-chain anchor.
7. **`Info`**: An action with no ledger effect, typically used to gauge community opinion or publish announcements.

---

## Filtering Governance Events

Governance events support three Cardano-specific pipeline filters:

- **DRep Filtering (`--filter-drep`)**: Matches events involving a specific DRep (DRep registration/update/deregistration certificates, vote delegation certificates targeting that DRep, and voting procedures where the voter is that DRep). Supports hex or bech32 form (including script DRep IDs).
- **Pool Filtering (`--filter-pool`)**: Matches events involving a stake pool (voting procedures cast by the pool as an SPO, and vote-delegation certificates referencing the pool's key hash).
- **Address Filtering (`--filter-address`)**: Matches addresses involved in the governance action (reward accounts of proposals, treasury withdrawal destination addresses, or delegating stake credentials).

### Filtering Examples

#### Command Line
```bash
# Filter on a DRep using Bech32
./adder --filter-type input.governance \
  --filter-drep drep1p4h4ea7y70ede2wy7x3t83x4umm63wwq68308f94cmt7szexmnr

# Filter on multiple addresses (comma-separated list, OR matching)
./adder --filter-type input.governance \
  --filter-address stake1u9f9v0z5zzlldgx58n8tklphu8mf7h4jvp2j2gddluemnssjfnkzz,stake1ux7abcd...
```

#### YAML Configuration
```yaml
filter:
  cardano:
    type: "input.governance"
    drep: "drep1p4h4ea7y70ede2wy7x3t83x4umm63wwq68308f94cmt7szexmnr"
    address: "stake1u9f9v0z5zzlldgx58n8tklphu8mf7h4jvp2j2gddluemnssjfnkzz"
```

---

## Example JSON Output

Below is an example of an `input.governance` event payload representing a **DRep Registration Certificate**:

```json
{
  "timestamp": "2026-07-17T21:40:00Z",
  "type": "input.governance",
  "context": {
    "transactionHash": "58200a1ad4abcd7290bc9831d102e34fa9de1e0287a98bcdef1238b1f20a67bc",
    "blockNumber": 10528430,
    "slotNumber": 68493120,
    "transactionIdx": 3,
    "networkMagic": 764824073
  },
  "payload": {
    "blockHash": "13aa2accf2e1561723aa26871e071fdf32c867cff7e7d50ad470d62fdeadbeef",
    "drepCertificates": [
      {
        "certificateType": "Registration",
        "drepHash": "81f156d98e1f02123abccdef5439a89d71fa9d8b76c8db028c7df0e1",
        "drepId": "drep1p4h4ea7y70ede2wy7x3t83x4umm63wwq68308f94cmt7szexmnr",
        "deposit": 500000000,
        "anchor": {
          "url": "https://example.com/drep-metadata.json",
          "dataHash": "58200a1ad4abcd7290bc9831d102e34fa9de1e0287a98bcdef1238b1f20a67bc"
        }
      }
    ]
  }
}
```
