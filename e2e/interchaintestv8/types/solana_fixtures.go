package types

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/suite"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/codec/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"

	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	channeltypesv2 "github.com/cosmos/ibc-go/v10/modules/core/04-channel/v2/types"
	ibcexported "github.com/cosmos/ibc-go/v10/modules/core/exported"
	ibctmtypes "github.com/cosmos/ibc-go/v10/modules/light-clients/07-tendermint"
	ibctesting "github.com/cosmos/ibc-go/v10/testing"

	"github.com/strangelove-ventures/interchaintest/v8/chain/cosmos"

	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/e2esuite"
	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/testvalues"

	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types"
	ics23 "github.com/cosmos/ics23/go"
)

type SolanaFixtureGenerator struct {
	Enabled    bool
	FixtureDir string
	suite      *suite.Suite
}

func NewSolanaFixtureGenerator(s *suite.Suite) *SolanaFixtureGenerator {
	generator := &SolanaFixtureGenerator{
		Enabled: os.Getenv(testvalues.EnvKeyGenerateSolanaFixtures) == testvalues.EnvValueGenerateFixtures_True,
		suite:   s,
	}

	if generator.Enabled {
		absPath, err := filepath.Abs(filepath.Join("../..", testvalues.SolanaFixturesDir))
		if err != nil {
			s.T().Fatalf("Failed to get absolute path for fixtures: %v", err)
		}
		generator.FixtureDir = absPath

		if err := os.MkdirAll(generator.FixtureDir, 0o755); err != nil {
			s.T().Fatalf("Failed to create Solana fixture directory: %v", err)
		}
		s.T().Logf("📁 Solana fixtures will be saved to: %s", generator.FixtureDir)
	}

	return generator
}

// GenerateMultipleUpdateClientScenarios generates multiple test scenarios
func (g *SolanaFixtureGenerator) GenerateMultipleUpdateClientScenarios(ctx context.Context, chainA *cosmos.CosmosChain, updateTxBodyBz []byte) {
	if !g.Enabled {
		return
	}

	g.suite.T().Log("🔧 Generating multiple update client scenarios")

	// Extract the real update client message from the transaction
	g.suite.T().Log("🔍 Parsing update client transaction")
	msgUpdateClient := g.extractUpdateClientMessage(updateTxBodyBz)
	g.suite.T().Logf("📊 Found MsgUpdateClient for client: %s", msgUpdateClient.ClientId)

	// Generate the happy path scenario using real transaction data
	g.generateHappyPathScenario(ctx, chainA, msgUpdateClient.ClientMessage)

	// Generate malformed client message scenario based on the real data
	g.generateMalformedClientMessageScenario(ctx, chainA)

	// Generate additional edge case scenarios
	g.generateExpiredHeaderScenario(ctx, chainA)
	g.generateFutureTimestampScenario(ctx, chainA)
	g.generateWrongTrustedHeightScenario(ctx, chainA)
	g.generateInvalidProtobufScenario()
	// Note: Conflicting consensus state scenario is complex to generate with valid signatures

	g.suite.T().Log("✅ Multiple Solana scenarios generated successfully")
}

func (g *SolanaFixtureGenerator) extractUpdateClientMessage(txBodyBz []byte) *clienttypes.MsgUpdateClient {
	var txBody txtypes.TxBody
	err := proto.Unmarshal(txBodyBz, &txBody)
	g.suite.Require().NoError(err)
	g.suite.Require().Len(txBody.Messages, 1, "Expected exactly one message in update client tx")

	var msgUpdateClient clienttypes.MsgUpdateClient
	err = proto.Unmarshal(txBody.Messages[0].Value, &msgUpdateClient)
	g.suite.Require().NoError(err)
	g.suite.Require().NotNil(msgUpdateClient.ClientMessage)

	return &msgUpdateClient
}

func (g *SolanaFixtureGenerator) generateHappyPathScenario(ctx context.Context, chainA *cosmos.CosmosChain, clientMessage *types.Any) {
	g.suite.T().Log("🔧 Generating happy path scenario")

	// Get the client state
	tmClientState := g.queryTendermintClientState(ctx, chainA)
	solanaClientState := g.convertClientStateToSolanaFormat(tmClientState, chainA.Config().ChainID)

	// Get the consensus state (this would be the trusted state)
	tmConsensusState := g.queryTendermintConsensusState(ctx, chainA)
	solanaConsensusState := g.convertConsensusStateToSolanaFormat(tmConsensusState, chainA.Config().ChainID)

	// Process the real update client message from the transaction
	realUpdateMessage := g.convertUpdateClientMessageToSolanaFormat(clientMessage)

	// Create the unified fixture
	unifiedFixture := map[string]interface{}{
		"scenario":                "happy_path",
		"client_state":            solanaClientState,
		"trusted_consensus_state": solanaConsensusState,
		"update_client_message":   realUpdateMessage,
		"metadata":                g.createUnifiedMetadata("happy_path", tmClientState.ChainId),
	}

	filename := filepath.Join(g.FixtureDir, "update_client_happy_path.json")
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Happy path scenario fixture saved: %s", filename)
}

func (g *SolanaFixtureGenerator) generateMalformedClientMessageScenario(ctx context.Context, chainA *cosmos.CosmosChain) {
	g.suite.T().Log("🔧 Generating malformed client message scenario")

	// Get valid client state and consensus state (same as happy path)
	tmClientState := g.queryTendermintClientState(ctx, chainA)
	solanaClientState := g.convertClientStateToSolanaFormat(tmClientState, chainA.Config().ChainID)

	tmConsensusState := g.queryTendermintConsensusState(ctx, chainA)
	solanaConsensusState := g.convertConsensusStateToSolanaFormat(tmConsensusState, chainA.Config().ChainID)

	// Load the happy path fixture to base the malformed one on
	happyPathFile := filepath.Join(g.FixtureDir, "update_client_happy_path.json")
	g.suite.Require().FileExists(happyPathFile, "Happy path fixture must exist before generating malformed fixture")

	g.suite.T().Log("📖 Loading happy path fixture to create malformed version")
	validHex := g.extractHexFromHappyPathFixture(happyPathFile)

	malformedHex := g.corruptSignatureInValidHeader(validHex)

	// Create a malformed update message by corrupting signature bytes from a valid message
	malformedUpdateMessage := map[string]interface{}{
		"client_message_hex": malformedHex,
		"type_url":           "/ibc.lightclients.tendermint.v1.Header",
		"trusted_height":     tmClientState.LatestHeight.RevisionHeight,
		"new_height":         tmClientState.LatestHeight.RevisionHeight + 1,
		"metadata":           g.createMetadata("Intentionally malformed Tendermint header for unhappy path testing (signature corruption in valid protobuf structure)"),
	}

	// Create the unified fixture
	unifiedFixture := map[string]interface{}{
		"scenario":                "malformed_client_message",
		"client_state":            solanaClientState,
		"trusted_consensus_state": solanaConsensusState,
		"update_client_message":   malformedUpdateMessage,
		"metadata":                g.createUnifiedMetadata("malformed_client_message", tmClientState.ChainId),
	}

	filename := filepath.Join(g.FixtureDir, "update_client_malformed_client_message.json")
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Malformed client message scenario fixture saved: %s", filename)
}

func (g *SolanaFixtureGenerator) convertUpdateClientMessageToSolanaFormat(clientMessage *types.Any) map[string]interface{} {
	headerBytes := clientMessage.Value

	// Parse the header to extract the new height information
	var tmHeader ibctmtypes.Header
	err := proto.Unmarshal(headerBytes, &tmHeader)
	g.suite.Require().NoError(err, "Failed to parse header for height extraction - fixture generation cannot continue")

	// Validate that we have valid height information
	trustedHeight := tmHeader.TrustedHeight.RevisionHeight
	newHeight := tmHeader.Header.Height

	g.suite.Require().Greater(newHeight, int64(0), "New height must be greater than 0")
	g.suite.Require().Greater(trustedHeight, uint64(0), "Trusted height must be greater than 0")
	g.suite.Require().Greater(newHeight, int64(trustedHeight), "New height must be greater than trusted height")

	return map[string]interface{}{
		"client_message_hex": hex.EncodeToString(headerBytes),
		"type_url":           clientMessage.TypeUrl,
		"trusted_height":     trustedHeight,
		"new_height":         newHeight,
		"metadata":           g.createMetadata("Protobuf-encoded Tendermint header for update client"),
	}
}

// extractHexFromHappyPathFixture loads the happy path fixture and extracts the client_message_hex
func (g *SolanaFixtureGenerator) extractHexFromHappyPathFixture(filePath string) string {
	data, err := os.ReadFile(filePath)
	g.suite.Require().NoError(err, "Failed to read happy path fixture")

	var fixture map[string]interface{}
	err = json.Unmarshal(data, &fixture)
	g.suite.Require().NoError(err, "Failed to parse happy path fixture JSON")

	updateMessage, ok := fixture["update_client_message"].(map[string]interface{})
	g.suite.Require().True(ok, "update_client_message not found in happy path fixture")

	hex, ok := updateMessage["client_message_hex"].(string)
	g.suite.Require().True(ok, "client_message_hex not found in happy path fixture")

	return hex
}

// corruptSignatureInValidHeader takes a valid header hex and corrupts signature bytes
// This creates a valid protobuf structure that will deserialize correctly but fail cryptographic verification
func (g *SolanaFixtureGenerator) corruptSignatureInValidHeader(validHex string) string {
	// Decode the hex string to bytes
	headerBytes, err := hex.DecodeString(validHex)
	if err != nil {
		g.suite.T().Fatalf("Failed to decode valid header hex: %v", err)
	}

	// Parse the header first to understand its structure
	var tmHeader ibctmtypes.Header
	err = proto.Unmarshal(headerBytes, &tmHeader)
	if err != nil {
		g.suite.T().Fatalf("Failed to parse header for corruption: %v", err)
	}

	// Make a copy to avoid modifying the original
	corruptedHeader := tmHeader

	// Corrupt signature data in the commit while preserving the protobuf structure
	if corruptedHeader.SignedHeader != nil && corruptedHeader.Commit != nil {
		commit := corruptedHeader.Commit

		// Corrupt block signature if it exists
		if len(commit.Signatures) > 0 {
			// Corrupt the first signature by flipping one byte
			if len(commit.Signatures[0].Signature) > 10 {
				// Flip a byte in the middle of the signature
				sigPos := len(commit.Signatures[0].Signature) / 2
				commit.Signatures[0].Signature[sigPos] ^= 0xFF
				g.suite.T().Logf("🔧 Corrupted signature byte at position %d in first commit signature", sigPos)
			}
		}

		// Also corrupt the block ID hash if present
		if len(commit.BlockID.Hash) > 0 {
			// Flip one byte in the block hash
			hashPos := len(commit.BlockID.Hash) / 2
			commit.BlockID.Hash[hashPos] ^= 0xFF
			g.suite.T().Logf("🔧 Corrupted block hash byte at position %d", hashPos)
		}
	}

	// Re-marshal the corrupted header
	corruptedBytes, err := proto.Marshal(&corruptedHeader)
	if err != nil {
		g.suite.T().Fatalf("Failed to marshal corrupted header: %v", err)
	}

	// Verify it can still be parsed (should succeed)
	var testHeader ibctmtypes.Header
	err = proto.Unmarshal(corruptedBytes, &testHeader)
	if err != nil {
		g.suite.T().Fatalf("Corrupted header failed to parse - corruption was too aggressive: %v", err)
	}

	g.suite.T().Log("🔧 Header corrupted successfully - still deserializable but signatures are invalid")
	return hex.EncodeToString(corruptedBytes)
}

func (g *SolanaFixtureGenerator) queryTendermintClientState(ctx context.Context, chainA *cosmos.CosmosChain) *ibctmtypes.ClientState {
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.suite.Require().NoError(err)
	g.suite.Require().NotNil(resp.ClientState)

	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(resp.ClientState.Value, &tmClientState)
	g.suite.Require().NoError(err)

	return &tmClientState
}

func (g *SolanaFixtureGenerator) convertClientStateToSolanaFormat(tmClientState *ibctmtypes.ClientState, chainID string) map[string]interface{} {
	return map[string]interface{}{
		"chain_id":                tmClientState.ChainId,
		"trust_level_numerator":   tmClientState.TrustLevel.Numerator,
		"trust_level_denominator": tmClientState.TrustLevel.Denominator,
		"trusting_period":         tmClientState.TrustingPeriod.Seconds(),
		"unbonding_period":        tmClientState.UnbondingPeriod.Seconds(),
		"max_clock_drift":         tmClientState.MaxClockDrift.Seconds(),
		"frozen_height":           tmClientState.FrozenHeight.RevisionHeight,
		"latest_height":           tmClientState.LatestHeight.RevisionHeight,
		"metadata":                g.createMetadata(fmt.Sprintf("Client state for %s captured from %s", tmClientState.ChainId, chainID)),
	}
}

func (g *SolanaFixtureGenerator) queryTendermintConsensusState(ctx context.Context, chainA *cosmos.CosmosChain) *ibctmtypes.ConsensusState {
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, chainA, &clienttypes.QueryConsensusStateRequest{
		ClientId:       ibctesting.FirstClientID,
		RevisionNumber: 1,
		RevisionHeight: 1,
		LatestHeight:   true,
	})
	g.suite.Require().NoError(err)
	g.suite.Require().NotNil(resp.ConsensusState)

	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(resp.ConsensusState.Value, &tmConsensusState)
	g.suite.Require().NoError(err)

	return &tmConsensusState
}

func (g *SolanaFixtureGenerator) convertConsensusStateToSolanaFormat(tmConsensusState *ibctmtypes.ConsensusState, chainID string) map[string]interface{} {
	return map[string]interface{}{
		"timestamp":            tmConsensusState.Timestamp.UnixNano(),
		"root":                 hex.EncodeToString(tmConsensusState.Root.GetHash()),
		"next_validators_hash": hex.EncodeToString(tmConsensusState.NextValidatorsHash),
		"metadata":             g.createMetadata(fmt.Sprintf("Consensus state captured from %s", chainID)),
	}
}

func (g *SolanaFixtureGenerator) saveJsonFixture(filename string, data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	g.suite.Require().NoError(err)

	err = os.WriteFile(filename, jsonData, 0o600)
	g.suite.Require().NoError(err)
}

func (g *SolanaFixtureGenerator) createMetadata(description string) map[string]interface{} {
	return map[string]interface{}{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"source":       "real_cosmos_chain",
		"description":  description,
	}
}

func (g *SolanaFixtureGenerator) createUnifiedMetadata(scenarioName, chainID string) map[string]interface{} {
	return map[string]interface{}{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"source":       "real_cosmos_chain",
		"description":  fmt.Sprintf("Unified update client fixture for scenario: %s", scenarioName),
		"scenario":     scenarioName,
		"chain_id":     chainID,
	}
}

// generateExpiredHeaderScenario creates a fixture with an expired header (beyond trusting period)
func (g *SolanaFixtureGenerator) generateExpiredHeaderScenario(ctx context.Context, chainA *cosmos.CosmosChain) {
	g.suite.T().Log("🔧 Generating expired header scenario")

	// Get valid client state and consensus state
	tmClientState := g.queryTendermintClientState(ctx, chainA)
	solanaClientState := g.convertClientStateToSolanaFormat(tmClientState, chainA.Config().ChainID)

	tmConsensusState := g.queryTendermintConsensusState(ctx, chainA)
	solanaConsensusState := g.convertConsensusStateToSolanaFormat(tmConsensusState, chainA.Config().ChainID)

	// Load the happy path fixture to base the expired one on
	happyPathFile := filepath.Join(g.FixtureDir, "update_client_happy_path.json")
	g.suite.Require().FileExists(happyPathFile)

	validHex := g.extractHexFromHappyPathFixture(happyPathFile)

	// Create an expired header by modifying the timestamp
	expiredHex := g.createExpiredHeader(validHex, int64(tmClientState.TrustingPeriod.Seconds()))

	expiredUpdateMessage := map[string]interface{}{
		"client_message_hex": expiredHex,
		"type_url":           "/ibc.lightclients.tendermint.v1.Header",
		"trusted_height":     tmClientState.LatestHeight.RevisionHeight,
		"new_height":         tmClientState.LatestHeight.RevisionHeight + 1,
		"metadata":           g.createMetadata("Expired header - timestamp beyond trusting period"),
	}

	unifiedFixture := map[string]interface{}{
		"scenario":                "expired_header",
		"client_state":            solanaClientState,
		"trusted_consensus_state": solanaConsensusState,
		"update_client_message":   expiredUpdateMessage,
		"metadata":                g.createUnifiedMetadata("expired_header", tmClientState.ChainId),
	}

	filename := filepath.Join(g.FixtureDir, "update_client_expired_header.json")
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Expired header scenario fixture saved: %s", filename)
}

// generateFutureTimestampScenario creates a fixture with a future timestamp
func (g *SolanaFixtureGenerator) generateFutureTimestampScenario(ctx context.Context, chainA *cosmos.CosmosChain) {
	g.suite.T().Log("🔧 Generating future timestamp scenario")

	tmClientState := g.queryTendermintClientState(ctx, chainA)
	solanaClientState := g.convertClientStateToSolanaFormat(tmClientState, chainA.Config().ChainID)

	tmConsensusState := g.queryTendermintConsensusState(ctx, chainA)
	solanaConsensusState := g.convertConsensusStateToSolanaFormat(tmConsensusState, chainA.Config().ChainID)

	happyPathFile := filepath.Join(g.FixtureDir, "update_client_happy_path.json")
	g.suite.Require().FileExists(happyPathFile)

	validHex := g.extractHexFromHappyPathFixture(happyPathFile)

	// Create a header with future timestamp (beyond max clock drift)
	futureHex := g.createFutureTimestampHeader(validHex, int64(tmClientState.MaxClockDrift.Seconds()))

	futureUpdateMessage := map[string]interface{}{
		"client_message_hex": futureHex,
		"type_url":           "/ibc.lightclients.tendermint.v1.Header",
		"trusted_height":     tmClientState.LatestHeight.RevisionHeight,
		"new_height":         tmClientState.LatestHeight.RevisionHeight + 1,
		"metadata":           g.createMetadata("Future timestamp - beyond max clock drift"),
	}

	unifiedFixture := map[string]interface{}{
		"scenario":                "future_timestamp",
		"client_state":            solanaClientState,
		"trusted_consensus_state": solanaConsensusState,
		"update_client_message":   futureUpdateMessage,
		"metadata":                g.createUnifiedMetadata("future_timestamp", tmClientState.ChainId),
	}

	filename := filepath.Join(g.FixtureDir, "update_client_future_timestamp.json")
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Future timestamp scenario fixture saved: %s", filename)
}

// generateWrongTrustedHeightScenario creates a fixture referencing wrong trusted height
func (g *SolanaFixtureGenerator) generateWrongTrustedHeightScenario(ctx context.Context, chainA *cosmos.CosmosChain) {
	g.suite.T().Log("🔧 Generating wrong trusted height scenario")

	tmClientState := g.queryTendermintClientState(ctx, chainA)
	solanaClientState := g.convertClientStateToSolanaFormat(tmClientState, chainA.Config().ChainID)

	tmConsensusState := g.queryTendermintConsensusState(ctx, chainA)
	solanaConsensusState := g.convertConsensusStateToSolanaFormat(tmConsensusState, chainA.Config().ChainID)

	happyPathFile := filepath.Join(g.FixtureDir, "update_client_happy_path.json")
	g.suite.Require().FileExists(happyPathFile)

	validHex := g.extractHexFromHappyPathFixture(happyPathFile)

	// Use the valid header but with wrong trusted height in metadata
	wrongHeightUpdateMessage := map[string]interface{}{
		"client_message_hex": validHex,
		"type_url":           "/ibc.lightclients.tendermint.v1.Header",
		"trusted_height":     tmClientState.LatestHeight.RevisionHeight + 100, // Wrong height
		"new_height":         tmClientState.LatestHeight.RevisionHeight + 1,
		"metadata":           g.createMetadata("Wrong trusted height - references non-existent consensus state"),
	}

	unifiedFixture := map[string]interface{}{
		"scenario":                "wrong_trusted_height",
		"client_state":            solanaClientState,
		"trusted_consensus_state": solanaConsensusState,
		"update_client_message":   wrongHeightUpdateMessage,
		"metadata":                g.createUnifiedMetadata("wrong_trusted_height", tmClientState.ChainId),
	}

	filename := filepath.Join(g.FixtureDir, "update_client_wrong_trusted_height.json")
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Wrong trusted height scenario fixture saved: %s", filename)
}

// generateInvalidProtobufScenario creates a fixture with invalid protobuf bytes
func (g *SolanaFixtureGenerator) generateInvalidProtobufScenario() {
	g.suite.T().Log("🔧 Generating invalid protobuf scenario")

	// Create completely invalid protobuf bytes
	invalidProtobuf := "FFFFFFFF" // Invalid protobuf that can't be decoded

	invalidUpdateMessage := map[string]interface{}{
		"client_message_hex": invalidProtobuf,
		"type_url":           "/ibc.lightclients.tendermint.v1.Header",
		"trusted_height":     19,
		"new_height":         20,
		"metadata":           g.createMetadata("Invalid protobuf bytes - cannot be deserialized"),
	}

	// Use dummy client and consensus states
	dummyClientState := map[string]interface{}{
		"chain_id":                "test-chain",
		"trust_level_numerator":   1,
		"trust_level_denominator": 3,
		"trusting_period":         1209600,
		"unbonding_period":        1814400,
		"max_clock_drift":         10,
		"frozen_height":           0,
		"latest_height":           19,
		"metadata":                g.createMetadata("Dummy client state for invalid protobuf test"),
	}

	dummyConsensusState := map[string]interface{}{
		"timestamp":            uint64(time.Now().Unix()),
		"root":                 hex.EncodeToString(make([]byte, 32)),
		"next_validators_hash": hex.EncodeToString(make([]byte, 32)),
		"metadata":             g.createMetadata("Dummy consensus state for invalid protobuf test"),
	}

	unifiedFixture := map[string]interface{}{
		"scenario":                "invalid_protobuf",
		"client_state":            dummyClientState,
		"trusted_consensus_state": dummyConsensusState,
		"update_client_message":   invalidUpdateMessage,
		"metadata":                g.createUnifiedMetadata("invalid_protobuf", "test-chain"),
	}

	filename := filepath.Join(g.FixtureDir, "update_client_invalid_protobuf.json")
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Invalid protobuf scenario fixture saved: %s", filename)
}

// Helper functions for modifying headers

func (g *SolanaFixtureGenerator) createExpiredHeader(validHex string, trustingPeriodSeconds int64) string {
	headerBytes, _ := hex.DecodeString(validHex)
	var header ibctmtypes.Header
	if err := proto.Unmarshal(headerBytes, &header); err != nil {
		g.suite.T().Fatalf("Failed to unmarshal header: %v", err)
	}

	// Set timestamp to be older than trusting period
	expiredTime := time.Now().Add(-time.Duration(trustingPeriodSeconds+3600) * time.Second) // Add 1 hour buffer
	header.Header.Time = expiredTime

	modifiedBytes, _ := proto.Marshal(&header)
	return hex.EncodeToString(modifiedBytes)
}

func (g *SolanaFixtureGenerator) createFutureTimestampHeader(validHex string, maxClockDriftSeconds int64) string {
	headerBytes, _ := hex.DecodeString(validHex)
	var header ibctmtypes.Header
	if err := proto.Unmarshal(headerBytes, &header); err != nil {
		g.suite.T().Fatalf("Failed to unmarshal header: %v", err)
	}

	// Set timestamp to be in the future beyond max clock drift
	futureTime := time.Now().Add(time.Duration(maxClockDriftSeconds+3600) * time.Second) // Add 1 hour buffer
	header.Header.Time = futureTime

	modifiedBytes, _ := proto.Marshal(&header)
	return hex.EncodeToString(modifiedBytes)
}

// GenerateMembershipVerificationScenarios generates fixtures for membership verification tests
func (g *SolanaFixtureGenerator) GenerateMembershipVerificationScenarios(ctx context.Context, chainA *cosmos.CosmosChain, packet channeltypesv2.Packet) {
	if !g.Enabled {
		return
	}

	g.suite.T().Log("🔧 Generating membership verification scenarios")

	// Generate happy path scenario with real packet commitment
	g.generateMembershipHappyPath(ctx, chainA, packet)

	// Generate unhappy path scenarios
	g.generateMembershipInvalidProof(ctx, chainA, packet)
	g.generateMembershipWrongPath(ctx, chainA, packet)
	g.generateMembershipWrongValue(ctx, chainA, packet)
	g.generateMembershipWrongHeight(ctx, chainA, packet)
	g.generateNonMembershipScenario(ctx, chainA)

	g.suite.T().Log("✅ Membership verification scenarios generated successfully")
}

// GenerateMembershipVerificationScenariosWithPredefinedKeys generates membership fixtures using predefined keys
func (g *SolanaFixtureGenerator) GenerateMembershipVerificationScenariosWithPredefinedKeys(ctx context.Context, chainA *cosmos.CosmosChain, keyPaths []string) {
	if !g.Enabled {
		return
	}
	g.suite.T().Log("🔧 Generating membership verification scenarios with predefined keys")

	for i, keyPath := range keyPaths {
		g.suite.T().Logf("🔍 Processing predefined key path: %s", keyPath)
		g.generateMembershipFixtureForKey(ctx, chainA, keyPath, i)
	}

	g.suite.T().Log("✅ Predefined key membership scenarios generated successfully")
}

// generateMembershipFixtureForKey generates a membership fixture for a specific predefined key
func (g *SolanaFixtureGenerator) generateMembershipFixtureForKey(ctx context.Context, chainA *cosmos.CosmosChain, keyPath string, index int) {
	g.suite.T().Logf("🔧 Generating membership fixture for key: %s", keyPath)

	// Get the current chain height for the query
	clientState, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.suite.Require().NoError(err)

	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(clientState.ClientState.Value, &tmClientState)
	g.suite.Require().NoError(err)
	currentHeight := tmClientState.LatestHeight.RevisionHeight

	// Query using ABCI with the predefined key path
	abciReq := &abci.RequestQuery{
		Path:   "store/" + string(ibcexported.StoreKey) + "/key",
		Data:   []byte(keyPath),
		Height: int64(currentHeight) - 1, // Use height-1 for proof generation
		Prove:  true,
	}

	g.suite.T().Logf("📡 ABCI Query: path=store/%s/key, data=%s, height=%d, prove=true", string(ibcexported.StoreKey), keyPath, abciReq.Height)

	abciResp, err := e2esuite.ABCIQuery(ctx, chainA, abciReq)
	g.suite.Require().NoError(err)

	if len(abciResp.Value) == 0 {
		g.suite.T().Logf("⚠️  ABCI value is empty for key: %s, skipping", keyPath)
		return
	}

	if len(abciResp.ProofOps.Ops) == 0 {
		g.suite.T().Logf("⚠️  ABCI proof is empty for key: %s, skipping", keyPath)
		return
	}

	g.suite.T().Logf("✅ ABCI query successful - value length: %d, proof ops: %d", len(abciResp.Value), len(abciResp.ProofOps.Ops))

	// TODO: Convert ABCI proof operations to IBC MerkleProof format
	// For now, use the original approach but add detailed logging to understand the structure
	g.suite.T().Logf("🔍 ABCI ProofOps analysis: %d operations", len(abciResp.ProofOps.Ops))
	for i, op := range abciResp.ProofOps.Ops {
		g.suite.T().Logf("   ProofOp[%d]: type=%s, key_len=%d, data_len=%d", i, op.Type, len(op.Key), len(op.Data))
		// Try to understand what's in op.Data
		if len(op.Data) > 0 {
			g.suite.T().Logf("   ProofOp[%d] data first 32 bytes: %x", i, op.Data[:min(32, len(op.Data))])
		}
	}

	// Convert ABCI ProofOps to IBC MerkleProof format
	proofBytes, err := g.convertABCIProofOpsToMerkleProof(abciResp.ProofOps)
	g.suite.Require().NoError(err)
	g.suite.T().Logf("📦 Converted ABCI ProofOps to IBC MerkleProof: %d bytes", len(proofBytes))

	// Get consensus state at the same height as the proof
	proofHeight := uint64(abciResp.Height + 1) // ABCI returns height-1, so add 1 for actual height

	// Query consensus state at the proof height (or find the closest available one)
	consensusStateResp, consensusErr := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, chainA, &clienttypes.QueryConsensusStateRequest{
		ClientId:       ibctesting.FirstClientID,
		RevisionNumber: 0,
		RevisionHeight: proofHeight,
	})

	// If consensus state doesn't exist at proof height, find the closest one
	if consensusErr != nil {
		g.suite.T().Logf("⚠️  No consensus state at proof height %d, finding closest available", proofHeight)

		// Query all consensus states to find the best match
		allConsensusStatesResp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStatesResponse](ctx, chainA, &clienttypes.QueryConsensusStatesRequest{
			ClientId: ibctesting.FirstClientID,
		})
		g.suite.Require().NoError(err)
		g.suite.Require().NotEmpty(allConsensusStatesResp.ConsensusStates, "No consensus states found for client")

		// Find the consensus state with height closest to but not exceeding proof height
		var bestMatch *clienttypes.ConsensusStateWithHeight
		for _, cs := range allConsensusStatesResp.ConsensusStates {
			if cs.Height.RevisionHeight <= proofHeight {
				if bestMatch == nil || cs.Height.RevisionHeight > bestMatch.Height.RevisionHeight {
					bestMatch = &cs
				}
			}
		}

		g.suite.Require().NotNil(bestMatch, "No suitable consensus state found")
		actualHeight := bestMatch.Height.RevisionHeight
		g.suite.T().Logf("🔍 Using consensus state at height %d (closest to proof height %d)", actualHeight, proofHeight)

		// Now query ABCI again with the consensus state height to get matching proof
		abciReq = &abci.RequestQuery{
			Path:   "store/" + string(ibcexported.StoreKey) + "/key",
			Data:   []byte(keyPath),
			Height: int64(actualHeight) - 1, // Use consensus state height for proof
			Prove:  true,
		}

		g.suite.T().Logf("📡 Re-querying ABCI with consensus state height: path=store/%s/key, data=%s, height=%d, prove=true",
			string(ibcexported.StoreKey), keyPath, abciReq.Height)

		abciResp, err = e2esuite.ABCIQuery(ctx, chainA, abciReq)
		g.suite.Require().NoError(err)
		g.suite.Require().NotEmpty(abciResp.Value, "ABCI value is empty after re-query")
		g.suite.Require().NotEmpty(abciResp.ProofOps.Ops, "ABCI proof is empty after re-query")

		// Update proof height to match consensus state
		proofHeight = actualHeight

		// Use the best match consensus state
		var tmConsensusState ibctmtypes.ConsensusState
		err = proto.Unmarshal(bestMatch.ConsensusState.Value, &tmConsensusState)
		g.suite.Require().NoError(err)

		// Store consensus state for later use
		consensusStateResp = &clienttypes.QueryConsensusStateResponse{
			ConsensusState: bestMatch.ConsensusState,
			ProofHeight:    bestMatch.Height,
		}
	}

	// Extract consensus state
	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(consensusStateResp.ConsensusState.Value, &tmConsensusState)
	g.suite.Require().NoError(err)

	// Create the membership proof message
	membershipMsg := map[string]interface{}{
		"height":             proofHeight,
		"delay_time_period":  0,
		"delay_block_period": 0,
		"proof":              hex.EncodeToString(proofBytes),
		"path":               []string{keyPath}, // Use the key path directly
		"value":              hex.EncodeToString(abciResp.Value),
		"metadata":           g.createMetadata(fmt.Sprintf("Valid membership proof for predefined key: %s", keyPath)),
	}

	// Get client state for context
	tmClientStatePtr := g.queryTendermintClientState(ctx, chainA)
	solanaClientState := g.convertClientStateToSolanaFormat(tmClientStatePtr, chainA.Config().ChainID)

	solanaConsensusState := map[string]interface{}{
		"timestamp":            tmConsensusState.Timestamp.UnixNano(),
		"root":                 hex.EncodeToString(tmConsensusState.Root.GetHash()),
		"next_validators_hash": hex.EncodeToString(tmConsensusState.NextValidatorsHash),
		"metadata":             g.createMetadata(fmt.Sprintf("Consensus state at height %d", proofHeight)),
	}

	unifiedFixture := map[string]interface{}{
		"scenario":        fmt.Sprintf("membership_predefined_key_%d", index),
		"client_state":    solanaClientState,
		"consensus_state": solanaConsensusState,
		"membership_msg":  membershipMsg,
		"key_info": map[string]interface{}{
			"path":        keyPath,
			"value_size":  len(abciResp.Value),
			"description": fmt.Sprintf("Predefined IBC key: %s", keyPath),
		},
		"metadata": g.createUnifiedMetadata(fmt.Sprintf("membership_predefined_key_%d", index), chainA.Config().ChainID),
	}

	filename := filepath.Join(g.FixtureDir, fmt.Sprintf("verify_membership_predefined_key_%d.json", index))
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Predefined key membership fixture saved: %s", filename)
}

// generateMembershipHappyPath generates a valid membership proof for a real packet
func (g *SolanaFixtureGenerator) generateMembershipHappyPath(ctx context.Context, chainA *cosmos.CosmosChain, packet channeltypesv2.Packet) {
	g.suite.T().Log("🔧 Generating membership happy path scenario")

	// Get the current chain height for the query
	clientState, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.suite.Require().NoError(err)

	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(clientState.ClientState.Value, &tmClientState)
	g.suite.Require().NoError(err)
	currentHeight := tmClientState.LatestHeight.RevisionHeight

	// Query the packet commitment with proof using ABCI
	g.suite.T().Logf("🔍 Querying packet commitment via ABCI for ClientId: %s, Sequence: %d, Height: %d", packet.SourceClient, packet.Sequence, currentHeight)
	abciResp, err := g.queryPacketCommitmentWithProof(ctx, chainA, packet.SourceClient, packet.Sequence, currentHeight)
	g.suite.Require().NoError(err)

	if len(abciResp.Value) == 0 {
		g.suite.T().Logf("❌ ABCI commitment value is empty for ClientId: %s, Sequence: %d", packet.SourceClient, packet.Sequence)
	} else {
		g.suite.T().Logf("✅ ABCI commitment found: %x", abciResp.Value)
	}

	if len(abciResp.ProofOps.Ops) == 0 {
		g.suite.T().Logf("❌ ABCI proof is empty for ClientId: %s, Sequence: %d", packet.SourceClient, packet.Sequence)
	} else {
		g.suite.T().Logf("✅ ABCI proof found with %d operations", len(abciResp.ProofOps.Ops))
	}

	g.suite.Require().NotEmpty(abciResp.Value, "Packet commitment value should not be empty")
	g.suite.Require().NotEmpty(abciResp.ProofOps.Ops, "Merkle proof should not be empty")
	g.suite.Require().NotZero(abciResp.Height, "Proof height should not be zero")

	// Get consensus state at proof height
	proofHeight := uint64(abciResp.Height + 1) // ABCI returns height-1, so add 1 for actual height
	consensusStateResp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, chainA, &clienttypes.QueryConsensusStateRequest{
		ClientId:       ibctesting.FirstClientID,
		RevisionNumber: 0, // Use revision number 0 for simd chains
		RevisionHeight: proofHeight,
	})
	g.suite.Require().NoError(err)

	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(consensusStateResp.ConsensusState.Value, &tmConsensusState)
	g.suite.Require().NoError(err)

	// TODO: Convert ABCI proof operations to IBC MerkleProof format
	// For now, use the original approach but add detailed logging to understand the structure
	g.suite.T().Logf("🔍 ABCI ProofOps analysis: %d operations", len(abciResp.ProofOps.Ops))
	for i, op := range abciResp.ProofOps.Ops {
		g.suite.T().Logf("   ProofOp[%d]: type=%s, key_len=%d, data_len=%d", i, op.Type, len(op.Key), len(op.Data))
		// Try to understand what's in op.Data
		if len(op.Data) > 0 {
			g.suite.T().Logf("   ProofOp[%d] data first 32 bytes: %x", i, op.Data[:min(32, len(op.Data))])
		}
	}

	// Convert ABCI ProofOps to IBC MerkleProof format
	proofBytes, err := g.convertABCIProofOpsToMerkleProof(abciResp.ProofOps)
	g.suite.Require().NoError(err)
	g.suite.T().Logf("📦 Converted ABCI ProofOps to IBC MerkleProof: %d bytes", len(proofBytes))

	// Build the commitment path using payload port information
	sourcePort := packet.Payloads[0].SourcePort
	destPort := packet.Payloads[0].DestinationPort
	commitmentPath := g.constructCommitmentPath(packet.Sequence, sourcePort, destPort)

	// Create the membership proof message
	membershipMsg := map[string]interface{}{
		"height":             proofHeight,
		"delay_time_period":  0,
		"delay_block_period": 0,
		"proof":              hex.EncodeToString(proofBytes),
		"path":               commitmentPath,
		"value":              hex.EncodeToString(abciResp.Value),
		"metadata":           g.createMetadata("Valid membership proof for packet commitment"),
	}

	// Get client state for context
	tmClientStatePtr := g.queryTendermintClientState(ctx, chainA)
	solanaClientState := g.convertClientStateToSolanaFormat(tmClientStatePtr, chainA.Config().ChainID)

	solanaConsensusState := map[string]interface{}{
		"timestamp":            tmConsensusState.Timestamp.UnixNano(),
		"root":                 hex.EncodeToString(tmConsensusState.Root.GetHash()),
		"next_validators_hash": hex.EncodeToString(tmConsensusState.NextValidatorsHash),
		"metadata":             g.createMetadata(fmt.Sprintf("Consensus state at height %d", proofHeight)),
	}

	unifiedFixture := map[string]interface{}{
		"scenario":        "membership_happy_path",
		"client_state":    solanaClientState,
		"consensus_state": solanaConsensusState,
		"membership_msg":  membershipMsg,
		"packet_info": map[string]interface{}{
			"sequence":         packet.Sequence,
			"source_port":      sourcePort,
			"destination_port": destPort,
		},
		"metadata": g.createUnifiedMetadata("membership_happy_path", tmClientState.ChainId),
	}

	filename := filepath.Join(g.FixtureDir, "verify_membership_happy_path.json")
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Membership happy path fixture saved: %s", filename)
}

// generateMembershipInvalidProof generates a fixture with corrupted proof bytes
func (g *SolanaFixtureGenerator) generateMembershipInvalidProof(ctx context.Context, chainA *cosmos.CosmosChain, packet channeltypesv2.Packet) {
	g.suite.T().Log("🔧 Generating membership invalid proof scenario")

	// Load the happy path fixture to get valid structure
	happyPathFile := filepath.Join(g.FixtureDir, "verify_membership_happy_path.json")
	g.suite.Require().FileExists(happyPathFile)

	data, err := os.ReadFile(happyPathFile)
	g.suite.Require().NoError(err)

	var happyFixture map[string]interface{}
	err = json.Unmarshal(data, &happyFixture)
	g.suite.Require().NoError(err)

	// Deep copy the fixture
	invalidFixture := g.deepCopyFixture(happyFixture)

	// Corrupt the proof
	membershipMsg := invalidFixture["membership_msg"].(map[string]interface{})
	proofHex := membershipMsg["proof"].(string)

	// Corrupt the middle of the proof
	proofBytes, _ := hex.DecodeString(proofHex)
	if len(proofBytes) > 20 {
		proofBytes[len(proofBytes)/2] ^= 0xFF
		proofBytes[len(proofBytes)/2+1] ^= 0xFF
	}

	membershipMsg["proof"] = hex.EncodeToString(proofBytes)
	membershipMsg["metadata"] = g.createMetadata("Corrupted merkle proof - should fail verification")

	invalidFixture["scenario"] = "membership_invalid_proof"
	invalidFixture["metadata"] = g.createUnifiedMetadata("membership_invalid_proof", happyFixture["client_state"].(map[string]interface{})["chain_id"].(string))

	filename := filepath.Join(g.FixtureDir, "verify_membership_invalid_proof.json")
	g.saveJsonFixture(filename, invalidFixture)
	g.suite.T().Logf("💾 Membership invalid proof fixture saved: %s", filename)
}

// generateMembershipWrongPath generates a fixture with incorrect commitment path
func (g *SolanaFixtureGenerator) generateMembershipWrongPath(ctx context.Context, chainA *cosmos.CosmosChain, packet channeltypesv2.Packet) {
	g.suite.T().Log("🔧 Generating membership wrong path scenario")

	happyPathFile := filepath.Join(g.FixtureDir, "verify_membership_happy_path.json")
	g.suite.Require().FileExists(happyPathFile)

	data, err := os.ReadFile(happyPathFile)
	g.suite.Require().NoError(err)

	var happyFixture map[string]interface{}
	err = json.Unmarshal(data, &happyFixture)
	g.suite.Require().NoError(err)

	wrongPathFixture := g.deepCopyFixture(happyFixture)

	// Use wrong sequence number in path
	sourcePort := packet.Payloads[0].SourcePort
	destPort := packet.Payloads[0].DestinationPort
	wrongPath := g.constructCommitmentPath(packet.Sequence+100, sourcePort, destPort)

	membershipMsg := wrongPathFixture["membership_msg"].(map[string]interface{})
	membershipMsg["path"] = wrongPath
	membershipMsg["metadata"] = g.createMetadata("Wrong commitment path - sequence number mismatch")

	wrongPathFixture["scenario"] = "membership_wrong_path"
	wrongPathFixture["metadata"] = g.createUnifiedMetadata("membership_wrong_path", happyFixture["client_state"].(map[string]interface{})["chain_id"].(string))

	filename := filepath.Join(g.FixtureDir, "verify_membership_wrong_path.json")
	g.saveJsonFixture(filename, wrongPathFixture)
	g.suite.T().Logf("💾 Membership wrong path fixture saved: %s", filename)
}

// generateMembershipWrongValue generates a fixture with incorrect commitment value
func (g *SolanaFixtureGenerator) generateMembershipWrongValue(ctx context.Context, chainA *cosmos.CosmosChain, packet channeltypesv2.Packet) {
	g.suite.T().Log("🔧 Generating membership wrong value scenario")

	happyPathFile := filepath.Join(g.FixtureDir, "verify_membership_happy_path.json")
	g.suite.Require().FileExists(happyPathFile)

	data, err := os.ReadFile(happyPathFile)
	g.suite.Require().NoError(err)

	var happyFixture map[string]interface{}
	err = json.Unmarshal(data, &happyFixture)
	g.suite.Require().NoError(err)

	wrongValueFixture := g.deepCopyFixture(happyFixture)

	// Use completely different commitment value
	wrongCommitment := make([]byte, 32)
	for i := range wrongCommitment {
		wrongCommitment[i] = 0xAB
	}

	membershipMsg := wrongValueFixture["membership_msg"].(map[string]interface{})
	membershipMsg["value"] = hex.EncodeToString(wrongCommitment)
	membershipMsg["metadata"] = g.createMetadata("Wrong commitment value - does not match packet")

	wrongValueFixture["scenario"] = "membership_wrong_value"
	wrongValueFixture["metadata"] = g.createUnifiedMetadata("membership_wrong_value", happyFixture["client_state"].(map[string]interface{})["chain_id"].(string))

	filename := filepath.Join(g.FixtureDir, "verify_membership_wrong_value.json")
	g.saveJsonFixture(filename, wrongValueFixture)
	g.suite.T().Logf("💾 Membership wrong value fixture saved: %s", filename)
}

// generateMembershipWrongHeight generates a fixture with incorrect proof height
func (g *SolanaFixtureGenerator) generateMembershipWrongHeight(ctx context.Context, chainA *cosmos.CosmosChain, packet channeltypesv2.Packet) {
	g.suite.T().Log("🔧 Generating membership wrong height scenario")

	happyPathFile := filepath.Join(g.FixtureDir, "verify_membership_happy_path.json")
	g.suite.Require().FileExists(happyPathFile)

	data, err := os.ReadFile(happyPathFile)
	g.suite.Require().NoError(err)

	var happyFixture map[string]interface{}
	err = json.Unmarshal(data, &happyFixture)
	g.suite.Require().NoError(err)

	wrongHeightFixture := g.deepCopyFixture(happyFixture)

	membershipMsg := wrongHeightFixture["membership_msg"].(map[string]interface{})
	currentHeight := membershipMsg["height"].(float64) // JSON unmarshals numbers as float64
	membershipMsg["height"] = uint64(currentHeight) + 1000
	membershipMsg["metadata"] = g.createMetadata("Wrong proof height - consensus state doesn't exist at this height")

	wrongHeightFixture["scenario"] = "membership_wrong_height"
	wrongHeightFixture["metadata"] = g.createUnifiedMetadata("membership_wrong_height", happyFixture["client_state"].(map[string]interface{})["chain_id"].(string))

	filename := filepath.Join(g.FixtureDir, "verify_membership_wrong_height.json")
	g.saveJsonFixture(filename, wrongHeightFixture)
	g.suite.T().Logf("💾 Membership wrong height fixture saved: %s", filename)
}

// generateNonMembershipScenario generates a non-membership proof fixture
func (g *SolanaFixtureGenerator) generateNonMembershipScenario(ctx context.Context, chainA *cosmos.CosmosChain) {
	g.suite.T().Log("🔧 Generating non-membership scenario")

	// Get the current chain height for the query
	clientState, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.suite.Require().NoError(err)

	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(clientState.ClientState.Value, &tmClientState)
	g.suite.Require().NoError(err)
	currentHeight := tmClientState.LatestHeight.RevisionHeight

	// Query for a non-existent packet (very high sequence number)
	nonExistentSequence := uint64(999999)

	// Query using ABCI - this should return proof of absence
	g.suite.T().Logf("🔍 Querying non-existent packet via ABCI for sequence: %d", nonExistentSequence)
	abciResp, err := g.queryPacketCommitmentWithProof(ctx, chainA, ibctesting.FirstClientID, nonExistentSequence, currentHeight)
	g.suite.Require().NoError(err)

	// For non-membership, value should be empty but proof should exist
	g.suite.Require().Empty(abciResp.Value, "Non-existent packet should have empty value")
	g.suite.Require().NotEmpty(abciResp.ProofOps.Ops, "Should have proof of absence")
	// Get consensus state at proof height
	proofHeight := uint64(abciResp.Height + 1) // ABCI returns height-1, so add 1 for actual height
	consensusStateResp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, chainA, &clienttypes.QueryConsensusStateRequest{
		ClientId:       ibctesting.FirstClientID,
		RevisionNumber: 0, // Use revision number 0 for simd chains
		RevisionHeight: proofHeight,
	})
	g.suite.Require().NoError(err)

	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(consensusStateResp.ConsensusState.Value, &tmConsensusState)
	g.suite.Require().NoError(err)

	// TODO: Convert ABCI proof operations to IBC MerkleProof format
	// For now, use the original approach but add detailed logging to understand the structure
	g.suite.T().Logf("🔍 ABCI ProofOps analysis: %d operations", len(abciResp.ProofOps.Ops))
	for i, op := range abciResp.ProofOps.Ops {
		g.suite.T().Logf("   ProofOp[%d]: type=%s, key_len=%d, data_len=%d", i, op.Type, len(op.Key), len(op.Data))
		// Try to understand what's in op.Data
		if len(op.Data) > 0 {
			g.suite.T().Logf("   ProofOp[%d] data first 32 bytes: %x", i, op.Data[:min(32, len(op.Data))])
		}
	}

	// Convert ABCI ProofOps to IBC MerkleProof format
	proofBytes, err := g.convertABCIProofOpsToMerkleProof(abciResp.ProofOps)
	g.suite.Require().NoError(err)
	g.suite.T().Logf("📦 Converted ABCI ProofOps to IBC MerkleProof: %d bytes", len(proofBytes))

	tmClientStatePtr2 := g.queryTendermintClientState(ctx, chainA)
	solanaClientState := g.convertClientStateToSolanaFormat(tmClientStatePtr2, chainA.Config().ChainID)

	solanaConsensusState := map[string]interface{}{
		"timestamp":            tmConsensusState.Timestamp.UnixNano(),
		"root":                 hex.EncodeToString(tmConsensusState.Root.GetHash()),
		"next_validators_hash": hex.EncodeToString(tmConsensusState.NextValidatorsHash),
		"metadata":             g.createMetadata(fmt.Sprintf("Consensus state at height %d", proofHeight)),
	}

	// Build the commitment path for non-existent packet
	commitmentPath := g.constructCommitmentPath(nonExistentSequence, "transfer", "transfer")

	nonMembershipMsg := map[string]interface{}{
		"height":             proofHeight,
		"delay_time_period":  0,
		"delay_block_period": 0,
		"proof":              hex.EncodeToString(proofBytes),
		"path":               commitmentPath,
		"value":              "", // Empty for non-membership
		"metadata":           g.createMetadata("Valid non-membership proof - packet doesn't exist"),
	}

	unifiedFixture := map[string]interface{}{
		"scenario":        "non_membership_happy_path",
		"client_state":    solanaClientState,
		"consensus_state": solanaConsensusState,
		"membership_msg":  nonMembershipMsg,
		"packet_info": map[string]interface{}{
			"sequence":            nonExistentSequence,
			"source_channel":      "transfer",
			"destination_channel": "transfer",
		},
		"metadata": g.createUnifiedMetadata("non_membership_happy_path", tmClientState.ChainId),
	}

	filename := filepath.Join(g.FixtureDir, "verify_non_membership_happy_path.json")
	g.saveJsonFixture(filename, unifiedFixture)
	g.suite.T().Logf("💾 Non-membership fixture saved: %s", filename)
}

// Helper function to construct ICS24 commitment path
func (g *SolanaFixtureGenerator) constructCommitmentPath(sequence uint64, sourceChannel, destChannel string) []string {
	return []string{
		"commitments",
		"ports",
		sourceChannel,
		"channels",
		destChannel,
		"sequences",
		fmt.Sprintf("%d", sequence),
	}
}

// queryPacketCommitmentWithProof queries packet commitment using ABCI to get merkle proof
func (g *SolanaFixtureGenerator) queryPacketCommitmentWithProof(ctx context.Context, chain *cosmos.CosmosChain, clientId string, sequence uint64, height uint64) (*abci.ResponseQuery, error) {
	// For IBC v2 (Eureka), construct the packet commitment path similar to SP1 tests
	// The path format follows: clients/{clientId}/packets/sequences/{sequence}
	packetCommitmentPath := fmt.Sprintf("clients/%s/packets/sequences/%d", clientId, sequence)

	// Use the proper IBC store key format, similar to how SP1 tests construct membershipKey
	// Format: [][]byte{[]byte(ibcexported.StoreKey), packetCommitmentKey}
	abciReq := &abci.RequestQuery{
		Path:   "store/" + string(ibcexported.StoreKey) + "/key",
		Data:   []byte(packetCommitmentPath),
		Height: int64(height) - 1, // Use height-1 for proof generation
		Prove:  true,
	}

	g.suite.T().Logf("📡 ABCI Query: path=store/%s/key, data=%s, height=%d, prove=true", string(ibcexported.StoreKey), packetCommitmentPath, abciReq.Height)

	return e2esuite.ABCIQuery(ctx, chain, abciReq)
}

// Helper function to deep copy fixture maps
func (g *SolanaFixtureGenerator) deepCopyFixture(src map[string]interface{}) map[string]interface{} {
	// Marshal and unmarshal to create a deep copy
	data, _ := json.Marshal(src)
	var dst map[string]interface{}
	json.Unmarshal(data, &dst)
	return dst
}

// convertABCIProofOpsToMerkleProof converts ABCI ProofOps format to IBC MerkleProof format
func (g *SolanaFixtureGenerator) convertABCIProofOpsToMerkleProof(proofOps *cmtcrypto.ProofOps) ([]byte, error) {
	g.suite.T().Logf("🔄 Converting %d ABCI ProofOps to IBC MerkleProof format", len(proofOps.Ops))

	// Each ProofOp contains ICS23 CommitmentProof data in op.Data
	// We need to extract these and create an IBC MerkleProof
	var commitmentProofs []*ics23.CommitmentProof

	for i, op := range proofOps.Ops {
		g.suite.T().Logf("   Processing ProofOp[%d]: type=%s, key_len=%d, data_len=%d",
			i, op.Type, len(op.Key), len(op.Data))

		// The op.Data contains the ICS23 CommitmentProof
		// Parse it as a CommitmentProof
		var commitmentProof ics23.CommitmentProof
		if err := proto.Unmarshal(op.Data, &commitmentProof); err != nil {
			g.suite.T().Logf("   ❌ Failed to unmarshal CommitmentProof from ProofOp[%d]: %v", i, err)
			return nil, fmt.Errorf("failed to unmarshal CommitmentProof from ProofOp[%d]: %w", i, err)
		}

		g.suite.T().Logf("   ✅ Successfully parsed CommitmentProof from ProofOp[%d]", i)
		commitmentProofs = append(commitmentProofs, &commitmentProof)
	}

	// Create IBC MerkleProof with the extracted CommitmentProofs
	merkleProof := &commitmenttypes.MerkleProof{
		Proofs: commitmentProofs,
	}

	// Marshal the MerkleProof to bytes
	proofBytes, err := proto.Marshal(merkleProof)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal MerkleProof: %w", err)
	}

	g.suite.T().Logf("✅ Successfully converted ABCI ProofOps to IBC MerkleProof: %d bytes", len(proofBytes))
	return proofBytes, nil
}
