# Lumerin node MOR-RPC replay — PoC

Signed consumer → provider MOR-RPC messages carry **no nonce, no
timestamp-freshness check, and no request-ID dedup**, and the transport
is **plaintext TCP**. A network observer can capture the exact signed
bytes of a `session.prompt` (or `agent.call_tool`) and re-deliver them
verbatim to the provider's public `:3333`. The provider accepts the
replay while the session is live: it re-runs inference, sends the
completion back over the replayer's TCP connection, and appends the
token counts to the session stats — so the provider's signed session
report grows, and the on-chain claim pays from the victim's escrowed
session stake.

## What this proves

`replay_r20_test.go` is a Go test for the Lumerin node
(`Morpheus-Lumerin-Node`, v7.19.0, commit `ba14d2fd`). It runs the
repo's own test harness (`newReportTestController` +
`MORRPCController.Handle`) and proves the replay property on the
shared auth path:

- `TestReplaySessionReportAcceptedTwice` — one signed `session.report`
  message, serialised once, delivered byte-identically twice through
  `Handle`; **both deliveries accepted**, both return provider-signed
  reports.
- `TestStaleTimestampSessionReportAccepted` — a request signed with a
  **2020-01-01** timestamp is accepted, proving no freshness check.

The `session.prompt` double-billing is code-level (same auth sequence
— `VerifySignature` + `isSessionValid`, no dedup anywhere in
`proxy-router`; `session.AddStats` unconditionally appends token
counters; the signed session report sums them for the on-chain claim).
The stream path is NOT replayable (`AppendChunk` requires strictly
sequential chunk indices; `CreateSession` rejects a duplicate
streamID).

## Reproduce

```sh
git clone https://github.com/MorpheusAIs/Morpheus-Lumerin-Node.git
cd Morpheus-Lumerin-Node
git checkout ba14d2fd
cp /path/to/replay_r20_test.go proxy-router/internal/proxyapi/replay_r20_test.go
cd proxy-router
go test ./internal/proxyapi/ -run 'TestReplaySessionReportAcceptedTwice|TestStaleTimestampSessionReportAccepted' -v -count=1
```

Expected: `PASS`, both tests, no failures. (Go 1.25+; if `/tmp` is a
small tmpfs, set `GOTMPDIR` and `GOCACHE` under a writable directory,
e.g. `GOTMPDIR=$HOME/.go-tmp GOCACHE=$HOME/.go-tmp/gocache`.)

## Honest scope

- Precondition: **network position** (passive tap / MITM on the
  consumer↔provider path) + TCP reachability to the provider. No keys
  needed.
- The executed PoC proves the replay property on `session.report`
  (the lightest handler that runs end-to-end in the test harness).
  A full two-node end-to-end replay with real inference was **not**
  run (needs a running provider + backend); the prompt billing chain
  is cited line by line in the report instead.
- Impact is bounded by the escrowed stake of the replayed session.
  No specific live session amount is claimed.
- Assessed **Medium at best, plausibly Low** (MITM-gated). This is a
  distinct third Lumerin item: off-chain, protocol-level, in the Go
  node — not the on-chain approval issue or the TTL issue.
