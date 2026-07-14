# Adder Tray Filtering and Notification Semantics

## Purpose

This document describes how monitoring filters should behave from a user's
perspective. It focuses on the tray application and desktop notifications,
especially when a user follows DReps and stake pools.

The central principle is:

> A monitoring target answers "who or what do I care about?" A notification
> preference answers "which actions involving that target should alert me?"

These are separate choices. Adding more targets should normally broaden what
the user monitors, not make notifications nearly impossible to trigger.

## Cardano Roles

### DReps

A Delegated Representative (DRep) participates in Cardano governance on behalf
of ADA holders who delegate voting power to that DRep. A user may follow:

- the DRep to whom they delegated;
- their own DRep identity;
- a candidate they are evaluating;
- several DReps whose voting records they want to compare;
- a DRep whose registration status or activity is important to them.

The useful DRep events are:

- a vote cast by the DRep, including the governance action and vote choice;
- registration, update, or retirement of the DRep;
- a delegation to or away from the DRep, when that event is available;
- a new governance proposal that the followed DRep may vote on.

A proposal is not normally created "by" a DRep. Therefore, a new-proposal
alert is a general call to action for somebody following DReps, not proof that
the followed DRep participated in the proposal.

### Stake pools

A stake pool produces blocks and may participate in governance as an SPO. A
user may follow:

- the pool to which they delegated stake;
- a pool they operate;
- several pools they are comparing;
- pools whose production or governance activity they audit.

The useful pool events are:

- a block minted by the pool;
- pool registration, retirement, or parameter changes;
- an SPO governance vote cast by the pool;
- stake delegated to or away from the pool, when attributable data is
  available;
- operational or performance alerts derived from reliable chain data.

DReps and pools are independent Cardano actors. A DRep vote does not normally
contain a pool ID, and a block minted by a pool does not normally contain a
DRep ID. A user can care about both, but that does not imply that both must be
present in the same event.

## Recommended Mental Model

### Target groups use explicit connectors

Multiple values within one target group are alternatives. The user chooses
AND or OR between adjacent groups:

```text
(Wallet A OR Wallet B)
AND (DRep A OR DRep B)
OR (Pool X OR Pool Y)
AND (Asset T)
AND (Policy P)
```

AND uses normal Boolean precedence over OR. In this example the expression is
equivalent to `(Wallets AND DReps) OR (Pools AND Assets AND Policies)`.

The application evaluates the expression exactly as configured, even when
different event families make a combination unlikely or currently impossible.
For example, Pool AND Asset currently matches neither a block event nor a
transaction event because block events do not carry transaction assets and
transaction events do not carry the pool issuer. The UI must not silently
change that AND to OR.

Examples:

- Connecting DRep A OR Pool X means "notify me about DRep A or Pool X."
- Connecting DRep A AND Pool X means both must match the same event.
- Following DRep A and DRep B means "notify me when either DRep acts."
- Following Pool X and Pool Y means "notify me when either pool acts."

Existing configurations default missing connectors to OR for compatibility.
Selecting AND is an explicit instruction and may suppress all alerts when no
supported event can satisfy both groups.

### Notification preferences narrow each target

Event preferences should narrow the relevant target category:

```text
(DRep A OR DRep B) AND (votes enabled OR registration changes enabled)
```

For example, a user following DRep A with only "Votes cast" enabled should get
DRep A's vote alerts, but not DRep registration alerts or votes from DRep B.

Preferences that do not apply to a target should not create impossible AND
conditions. "Blocks minted" applies to pools, while "Votes cast" may apply to
both DReps and SPOs if the UI explicitly supports both kinds of vote.

### Use AND only for an explicit compound condition

AND is reasonable when the user deliberately constructs a relationship that
can occur in one event. Examples include:

- transactions involving Wallet A AND Asset T;
- transactions involving Wallet A AND Policy P;
- incoming transfers to Wallet A above a specified amount.

The UI exposes AND/OR directly between adjacent target groups. It does not
maintain a separate custom transaction-rule view.

## Expected Notification Matrix

| Followed target | Event                                          | Notify by default when enabled? | Identity match                                      |
| --------------- | ---------------------------------------------- | ------------------------------- | --------------------------------------------------- |
| DRep            | DRep casts a vote                              | Yes                             | Vote voter ID/hash is followed                      |
| DRep            | DRep registers, updates, or retires            | Yes                             | Certificate DRep ID/hash is followed                |
| DRep            | New governance proposal                        | Optional                        | Global event; clearly label it as not DRep-specific |
| DRep            | Stake is delegated to/from DRep                | Optional                        | Delegation references followed DRep                 |
| Pool            | Pool mints a block                             | Yes                             | Block issuer matches followed pool                  |
| Pool            | Pool registers, retires, or changes parameters | Yes when supported              | Certificate operator/pool ID is followed            |
| Pool            | Pool casts an SPO vote                         | Optional                        | SPO voter hash is followed pool                     |
| Pool            | Stake is delegated to/from pool                | Optional                        | Delegation references followed pool                 |
| Any             | Unrelated actor performs the action            | No                              | No followed identity matches                        |

"Optional" means the user should be able to enable or disable that alert type;
it does not mean that identity matching may be skipped.

## Common User Scenarios

### Delegator following one DRep and one pool

Configuration:

- DRep A;
- Pool X;
- DRep votes enabled;
- blocks minted enabled.

Expected behavior:

- DRep A votes: notify;
- Pool X mints a block: notify;
- DRep B votes: do not notify;
- Pool Y mints a block: do not notify;
- an event involving only DRep A must not also require Pool X.

### DRep operator

The operator follows their own DRep and enables votes plus registration
changes. They should be notified when that DRep votes or its certificate
changes. They should not receive alerts for all governance activity merely
because the event type is governance.

### Pool operator

The operator follows one or more pool IDs and enables block production,
registration/parameter changes, and optionally SPO votes. Each pool is an
alternative target. A block from any followed pool should notify exactly once.

### Researcher following several actors

A researcher may follow many DReps and pools. All IDs within each list and all
independent actor lists should OR together. Notification preferences then
control which kinds of activity enter the alert stream.

## Notification Quality

An alert should answer:

- what happened;
- which followed identity caused the match;
- the important action detail, such as Yes/No/Abstain or block number;
- the transaction, block, or governance action reference;
- whether the event is confirmed, pending, or rolled back when known.

When one chain event matches several rules, the application should avoid
duplicating equivalent desktop notifications. It may combine distinct facts
into one notification or emit separate notifications only when each conveys a
different useful action.

Rate limiting should coalesce bursts without changing filter semantics. A
summary such as "12 followed events occurred" is preferable to dropping alerts
silently.

## Input and Validation Expectations

- Accept DRep IDs in supported bech32 and hexadecimal forms.
- Accept pool IDs in bech32 and hexadecimal forms.
- Normalize equivalent forms before matching.
- Reject malformed IDs before saving the configuration.
- Prevent or clearly warn about IDs for the wrong target type.
- Respect the selected Cardano network where an identifier is network-aware.
- Show the saved target in a recognizable truncated form, with access to the
  complete value.

## Monitor Everything

"Monitor everything" should be a separate, understandable mode. It should
ignore target lists and emit enabled event families for the whole network. The
UI should make the potentially high notification volume clear.

Turning off "Monitor everything" should require at least one valid target.
Previously entered targets should not unexpectedly combine into hidden AND
conditions.

## ChainSync Event and Notification Inventory

This section distinguishes events emitted by the ChainSync input from desktop
notifications currently implemented by the tray. An event being present in
ChainSync does not automatically mean that the tray has a rule and useful
message for it.

### Events emitted by ChainSync

For each confirmed block, ChainSync emits events in this order:

1. one `input.block` event;
2. one `input.transaction` event for every transaction in the block;
3. one `input.governance` event for each transaction containing governance
   data;
4. one individual DRep certificate event for every DRep certificate in the
   transaction.

ChainSync also emits `input.rollback` when the chain rolls back.

The individual DRep certificate event types are:

- `chainsync.drep.registration`;
- `chainsync.drep.update`;
- `chainsync.drep.deregistration`.

The same DRep certificate is also represented inside the transaction's
`input.governance` event. Consumers must avoid producing duplicate alerts from
both representations.

### Transaction data available from ChainSync

Every confirmed `input.transaction` event includes:

- transaction hash, index, block hash, block number, and slot number;
- inputs and outputs;
- resolved input outputs when Kupo resolution succeeds;
- ADA and native assets in outputs;
- fee, TTL, withdrawals, metadata, reference inputs, and certificates when
  present;
- optional transaction CBOR.

This data can support these notification rules:

| Transaction notification                          | Current tray support                     | Notes                                                   |
| ------------------------------------------------- | ---------------------------------------- | ------------------------------------------------------- |
| Incoming ADA to a followed wallet                 | Yes                                      | Matches a followed address in outputs                   |
| Outgoing ADA from a followed wallet               | Yes, when inputs resolve                 | Requires followed address in resolved inputs            |
| Native-token transfer involving a followed wallet | Yes                                      | Requires wallet involvement and token output            |
| Activity for a followed asset fingerprint         | Yes                                      | Matches assets in transaction outputs                   |
| Activity for a followed policy ID                 | Yes                                      | Matches policies in transaction outputs                 |
| Any confirmed transaction                         | Yes, in Monitor Everything               | Usually too noisy for desktop use                       |
| Specific transaction ID                           | Not currently exposed                    | Transaction hash is available in context                |
| Minimum ADA/token amount                          | Not currently exposed                    | Amount data is available in outputs                     |
| Certificate-bearing transaction                   | Not currently exposed as a general alert | Certificates are present in the transaction event       |
| Withdrawal involving a followed reward account    | Not currently exposed                    | Withdrawal data is available                            |
| Metadata-based transaction                        | Not currently exposed                    | Metadata is available but requires a structured matcher |
| Transaction included by a followed pool           | Not currently supported directly         | Requires block issuer correlation described below       |

Outgoing matching depends on resolved inputs. If Kupo is unavailable or input
resolution fails, ChainSync still emits the transaction but the tray cannot
reliably determine that a followed wallet spent an input.

### DRep and governance data available from ChainSync

An `input.governance` event can contain:

- governance proposals;
- votes by DReps, SPOs, or constitutional committee members;
- DRep registration, update, and deregistration certificates;
- vote delegation certificates;
- combined stake and vote delegation certificates;
- vote registration delegation certificates;
- combined stake, vote, and registration delegation certificates;
- constitutional committee hot-key authorization and resignation
  certificates.

The current DRep-related notification coverage is:

| DRep/governance notification                             | Current tray support                  | Identity scope                                               |
| -------------------------------------------------------- | ------------------------------------- | ------------------------------------------------------------ |
| Followed DRep casts a vote                               | Yes                                   | Matches followed DRep voter ID/hash                          |
| Followed DRep registers                                  | Yes                                   | Reported as a registration change                            |
| Followed DRep updates its registration                   | Yes                                   | Reported as a registration change                            |
| Followed DRep deregisters/retires                        | Yes                                   | Reported as a registration change                            |
| New governance proposal                                  | Yes, optional                         | Global; not caused by the followed DRep                      |
| Stake credential delegates voting power to followed DRep | Not currently notified                | DRep identity is available in vote delegation data           |
| Stake credential changes away from followed DRep         | Not directly derivable from one event | Requires prior delegation state                              |
| Followed DRep inactivity or missed vote                  | Not currently derivable               | Requires proposal deadlines and historical state             |
| DRep voting-power change                                 | Not currently notified                | Requires delegation and stake-state aggregation              |
| Individual `chainsync.drep.*` event                      | Not consumed directly by tray rules   | Equivalent certificate is handled through `input.governance` |

The governance event also contains SPO and committee activity, but current
DRep rules intentionally match only the followed DRep for votes and
certificates.

### Pool data available from ChainSync

The `input.block` event includes:

- block issuer key hash, which identifies the minting pool;
- block hash, block number, slot, body size, and transaction count;
- optional block CBOR.

Transactions and governance events may additionally carry pool-related
certificates or SPO voter hashes. ChainSync therefore exposes data for more
pool actions than the tray currently alerts on.

| Pool notification                               | Current tray support                       | Available source                                                              |
| ----------------------------------------------- | ------------------------------------------ | ----------------------------------------------------------------------------- |
| Followed pool mints a block                     | Yes                                        | `input.block` issuer key hash                                                 |
| Chain rollback affecting observed blocks        | Yes                                        | `input.rollback`, gated by block notification preference                      |
| Followed pool casts an SPO governance vote      | Not currently notified                     | `input.governance.votingProcedures`                                           |
| Stake delegates to followed pool                | Not currently notified                     | Transaction certificate; combined forms also appear in governance data        |
| Pool registration                               | Not currently notified                     | Transaction certificate                                                       |
| Pool retirement                                 | Not currently notified                     | Transaction certificate                                                       |
| Pool parameter update                           | Not currently notified                     | Pool registration/update certificate data needs a dedicated usable projection |
| Pool rewards                                    | Not emitted as a dedicated ChainSync event | Requires reward-account/epoch-state data                                      |
| Pool performance or missed blocks               | Not directly emitted                       | Requires schedule and historical analysis                                     |
| Transaction included in a followed pool's block | Not currently notified                     | Block and transaction share block hash, but transaction lacks issuer ID       |

The existing "Pool parameter changes" notification preference has no working
tray rule. It should remain hidden or marked unavailable until an appropriate
event projection and matcher exist.

### Pool and transaction correlation

ChainSync emits the block before its transactions. The block event contains
both the block hash and issuer key hash; each confirmed transaction contains
the same block hash. The information needed to correlate them therefore exists
across two events, but not in one transaction event.

There are two reasonable implementation options:

1. Add the block issuer/pool key hash to each confirmed transaction context or
   payload when ChainSync constructs it. This makes compound matching
   stateless and is the simpler model.
2. Cache `blockHash -> issuerVkey` in the notification engine and join each
   transaction to the preceding block event. This avoids changing the event
   schema but adds state, ordering, expiry, reconnect, and rollback concerns.

Adding issuer identity to the confirmed transaction event is preferable. A
pool-inclusion rule can then require:

```text
transaction.blockIssuer is one of the followed pools
AND transaction matches a wallet, transaction ID, asset, policy, or amount
```

Mempool transactions have no block hash or issuer and cannot match this rule.

### Complete current desktop notification set

For the requested DRep, pool, and transaction scope, the tray can currently
produce these desktop notifications:

1. incoming transaction for a followed wallet;
2. outgoing transaction for a followed wallet when inputs are resolved;
3. token transfer involving a followed wallet;
4. followed asset activity;
5. followed policy activity;
6. followed DRep vote;
7. followed DRep registration, update, or deregistration as a registration
   change;
8. new governance proposal for users who enabled that DRep-related global
   alert;
9. block minted by a followed pool;
10. generic block, transaction, and governance notifications in Monitor
    Everything mode;
11. rollback notification when block notifications are enabled.

Connection notifications also exist, but they are synthesized by the tray and
are not ChainSync DRep, pool, or transaction events.

## Current Adder Behavior and Gaps

The tray application correctly owns its monitoring targets separately from the
sidecar Cardano filter configuration. This is important because the tray needs
notification-oriented semantics rather than a generic pipeline query.

Current tray rule behavior includes:

- one target-expression view shared by setup and notification rules;
- OR matching among multiple values inside each target group;
- persisted AND/OR connectors between Wallet, DRep, Pool, Asset, and Policy
  groups in Standard monitoring, evaluated with AND before OR;
- followed DRep votes matched by DRep ID/hash;
- followed DRep registration changes matched by DRep ID/hash;
- new proposals treated as target-independent governance alerts;
- blocks matched to followed pools by block issuer;
- notification preferences used to enable relevant rule families;
- rollback, connection, and rate-limit handling outside target identity rules.

Known gaps or inconsistencies:

1. "Pool parameter changes" is presented as a notification preference, but the
   notification rules intentionally do not create such an alert because Adder
   does not currently emit a suitable pool-parameter event.
2. Pool notification rules currently cover block production, not SPO votes,
   delegations, registrations, retirements, rewards, or parameter changes.
3. DRep notification rules currently cover votes and DRep certificate changes.
   Delegations to or away from a DRep are not exposed as a distinct tray alert.
4. The generic CLI Cardano filter documentation describes different filter
   categories as AND conditions. That can remain valid for explicit pipeline
   queries, but it should not define the tray's default target-following model.

Unsupported options should be hidden or marked unavailable rather than shown
as if they produce notifications.

## Recommended Product Contract

The tray should guarantee:

1. An event is eligible when it satisfies the configured expression across
   followed target groups.
2. Multiple values in one target category match when any value matches.
3. The relevant notification preference must be enabled.
4. Unrelated identities never match solely because they share an event type.
5. Compatible transaction constraints use AND only when the UI explicitly
   communicates that relationship.
6. One event does not produce duplicate equivalent notifications.
7. Unsupported event preferences are not offered as functional controls.

In short, connecting DRep A OR Pool X should mean:

> Notify me when an enabled event happens for DRep A or Pool X.

Connecting the same groups with AND instead requires both to match one event,
even when no currently supported event can do so.
