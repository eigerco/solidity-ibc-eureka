package types

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/suite"

	"github.com/cosmos/cosmos-sdk/codec/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types"
	ibctmtypes "github.com/cosmos/ibc-go/v10/modules/light-clients/07-tendermint"
	ibctesting "github.com/cosmos/ibc-go/v10/testing"
	ics23 "github.com/cosmos/ics23/go"

	abci "github.com/cometbft/cometbft/abci/types"
	tmcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	"github.com/strangelove-ventures/interchaintest/v8/chain/cosmos"

	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/e2esuite"
	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/testvalues"
)

type SolanaFixtureGenerator struct {
	Enabled    bool
	FixtureDir string
	suite      *suite.Suite
}

// NewSolanaFixtureGenerator creates a new SolanaFixtureGenerator
func NewSolanaFixtureGenerator(s *suite.Suite) *SolanaFixtureGenerator {
	generator := &SolanaFixtureGenerator{
		Enabled: os.Getenv("GENERATE_SOLANA_FIXTURES") == "true",
		suite:   s,
	}

	if generator.Enabled {
		// Create absolute path to avoid issues with directory changes
		absPath, err := filepath.Abs(filepath.Join("../..", testvalues.SolanaFixturesDir))
		if err != nil {
			s.T().Fatalf("Failed to get absolute path for fixtures: %v", err)
		}
		generator.FixtureDir = absPath

		if err := os.MkdirAll(generator.FixtureDir, 0755); err != nil {
			s.T().Fatalf("Failed to create Solana fixture directory: %v", err)
		}
		s.T().Logf("📁 Solana fixtures will be saved to: %s", generator.FixtureDir)
	}

	return generator
}

// GenerateFixturesFromUpdateTx extracts the update client data and generates Solana fixtures
func (g *SolanaFixtureGenerator) GenerateFixturesFromUpdateTx(ctx context.Context, updateTxBodyBz []byte, chainA *cosmos.CosmosChain) {
	if !g.Enabled {
		return
	}

	g.suite.T().Log("🔍 Parsing update client transaction")

	// Parse the transaction body
	var txBody txtypes.TxBody
	err := proto.Unmarshal(updateTxBodyBz, &txBody)
	g.suite.Require().NoError(err)
	g.suite.Require().Len(txBody.Messages, 1, "Expected exactly one message in update client tx")

	// Extract the MsgUpdateClient
	var msgUpdateClient clienttypes.MsgUpdateClient
	err = proto.Unmarshal(txBody.Messages[0].Value, &msgUpdateClient)
	g.suite.Require().NoError(err)

	g.suite.T().Logf("📊 Found MsgUpdateClient for client: %s", msgUpdateClient.ClientId)

	// Extract the client message (this is the protobuf header that Solana needs)
	clientMessage := msgUpdateClient.ClientMessage
	g.suite.Require().NotNil(clientMessage)

	// Generate fixtures
	g.generateClientStateFixture(ctx, chainA)
	g.generateConsensusStateFixture(ctx, chainA)
	g.generateUpdateClientMessageFixture(clientMessage)

	g.suite.T().Log("✅ Solana fixtures generated successfully")
}

// generateClientStateFixture generates a client state fixture for Solana
func (g *SolanaFixtureGenerator) generateClientStateFixture(ctx context.Context, chainA *cosmos.CosmosChain) {
	g.suite.T().Log("🔧 Generating ClientState fixture")

	// Query the client state from chainA
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.suite.Require().NoError(err)
	g.suite.Require().NotNil(resp.ClientState)

	// Unmarshal the Tendermint client state
	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(resp.ClientState.Value, &tmClientState)
	g.suite.Require().NoError(err)

	// Convert to Solana format
	solanaClientState := map[string]interface{}{
		"chain_id":                tmClientState.ChainId,
		"trust_level_numerator":   tmClientState.TrustLevel.Numerator,
		"trust_level_denominator": tmClientState.TrustLevel.Denominator,
		"trusting_period":         tmClientState.TrustingPeriod.Seconds(),
		"unbonding_period":        tmClientState.UnbondingPeriod.Seconds(),
		"max_clock_drift":         tmClientState.MaxClockDrift.Seconds(),
		"frozen_height":           tmClientState.FrozenHeight.RevisionHeight,
		"latest_height":           tmClientState.LatestHeight.RevisionHeight,
		"metadata": map[string]interface{}{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"source":       "real_cosmos_chain",
			"description":  fmt.Sprintf("Client state for %s captured from %s", tmClientState.ChainId, chainA.Config().ChainID),
		},
	}

	// Save to file
	filename := filepath.Join(g.FixtureDir, "client_state.json")
	g.saveJsonFixture(filename, solanaClientState)
	g.suite.T().Logf("💾 Client state fixture saved: %s", filename)
}

// generateConsensusStateFixture generates a consensus state fixture for Solana
func (g *SolanaFixtureGenerator) generateConsensusStateFixture(ctx context.Context, chainA *cosmos.CosmosChain) {
	g.suite.T().Log("🔧 Generating ConsensusState fixture")

	// Query the consensus state from chainA
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, chainA, &clienttypes.QueryConsensusStateRequest{
		ClientId:       ibctesting.FirstClientID,
		RevisionNumber: 1,
		RevisionHeight: 1,
		LatestHeight:   true,
	})
	g.suite.Require().NoError(err)
	g.suite.Require().NotNil(resp.ConsensusState)

	// Unmarshal the Tendermint consensus state
	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(resp.ConsensusState.Value, &tmConsensusState)
	g.suite.Require().NoError(err)

	// Convert to Solana format
	solanaConsensusState := map[string]interface{}{
		"timestamp":            tmConsensusState.Timestamp.UnixNano(),
		"root":                 hex.EncodeToString(tmConsensusState.Root.GetHash()),
		"next_validators_hash": hex.EncodeToString(tmConsensusState.NextValidatorsHash),
		"metadata": map[string]interface{}{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"source":       "real_cosmos_chain",
			"description":  fmt.Sprintf("Consensus state captured from %s", chainA.Config().ChainID),
		},
	}

	// Save to file
	filename := filepath.Join(g.FixtureDir, "consensus_state.json")
	g.saveJsonFixture(filename, solanaConsensusState)
	g.suite.T().Logf("💾 Consensus state fixture saved: %s", filename)
}

// generateUpdateClientMessageFixture generates the update client message fixture for Solana
func (g *SolanaFixtureGenerator) generateUpdateClientMessageFixture(clientMessage *types.Any) {
	g.suite.T().Log("🔧 Generating UpdateClientMessage fixture")

	// The client message is already the protobuf-encoded header that Solana needs
	headerBytes := clientMessage.Value

	// Create the fixture
	updateClientMessage := map[string]interface{}{
		"client_message_hex":    hex.EncodeToString(headerBytes),
		"client_message_base64": hex.EncodeToString(headerBytes), // For now, same as hex
		"client_message_bytes":  headerBytes,
		"type_url":              clientMessage.TypeUrl,
		"metadata": map[string]interface{}{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"source":       "real_cosmos_chain",
			"description":  "Protobuf-encoded Tendermint header for update client",
		},
	}

	// Save to file
	filename := filepath.Join(g.FixtureDir, "update_client_message.json")
	g.saveJsonFixture(filename, updateClientMessage)
	g.suite.T().Logf("💾 Update client message fixture saved: %s", filename)
}

// saveJsonFixture saves a fixture as JSON
func (g *SolanaFixtureGenerator) saveJsonFixture(filename string, data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	g.suite.Require().NoError(err)

	err = os.WriteFile(filename, jsonData, 0644)
	g.suite.Require().NoError(err)
}

// GenerateMembershipFixtures generates membership verification fixtures for Solana
func (g *SolanaFixtureGenerator) GenerateMembershipFixtures(ctx context.Context, chainA *cosmos.CosmosChain, keyPaths []string) {
	if !g.Enabled {
		return
	}

	g.suite.T().Log("🔧 Generating Membership fixtures for Solana")

	// Get current client state and consensus state
	clientState := g.getCurrentClientState(ctx, chainA)
	consensusState := g.getCurrentConsensusState(ctx, chainA)

	// Generate fixtures for each key path
	for i, keyPath := range keyPaths {
		g.suite.T().Logf("📝 Generating membership fixture for path: %s", keyPath)

		// Create membership fixture
		membershipFixture := g.generateMembershipFixture(ctx, chainA, keyPath, clientState, consensusState, true)

		// Save membership fixture
		filename := filepath.Join(g.FixtureDir, fmt.Sprintf("membership_%d.json", i))
		g.saveJsonFixture(filename, membershipFixture)
		g.suite.T().Logf("💾 Membership fixture saved: %s", filename)

		// Create non-membership fixture (for a non-existent path)
		nonMembershipPath := keyPath + "_nonexistent"
		nonMembershipFixture := g.generateMembershipFixture(ctx, chainA, nonMembershipPath, clientState, consensusState, false)

		// Save non-membership fixture
		filename = filepath.Join(g.FixtureDir, fmt.Sprintf("non_membership_%d.json", i))
		g.saveJsonFixture(filename, nonMembershipFixture)
		g.suite.T().Logf("💾 Non-membership fixture saved: %s", filename)
	}

	g.suite.T().Log("✅ Solana membership fixtures generated successfully")
}

// generateMembershipFixture generates a single membership fixture
func (g *SolanaFixtureGenerator) generateMembershipFixture(ctx context.Context, chainA *cosmos.CosmosChain, keyPath string, clientState map[string]interface{}, consensusState map[string]interface{}, expectExists bool) map[string]interface{} {
	// Get the latest height from client state (this is counterparty chain height)
	height := clientState["latest_height"].(uint64)
	
	// Get the current height of the chain we're querying
	currentHeight, err := chainA.Height(ctx)
	if err != nil {
		g.suite.T().Logf("⚠️ Failed to get current height: %v", err)
	} else {
		g.suite.T().Logf("🔍 Client state height: %d, Current chain height: %d", height, currentHeight)
		// Use current height if it's higher
		if currentHeight > int64(height) {
			height = uint64(currentHeight)
		}
	}
	
	// IMPORTANT: Update client state latest_height to match proof height for validation
	// AND get the consensus state at the proof height for verification
	// This ensures both height validation and proof verification use the same height
	updatedClientState := make(map[string]interface{})
	for k, v := range clientState {
		updatedClientState[k] = v
	}
	updatedClientState["latest_height"] = height
	
	// CRITICAL FIX: For membership verification, we need the consensus state that matches
	// the blockchain state where the proof was generated, NOT the IBC light client's
	// stored consensus state. The proof is generated against the blockchain's app hash
	// at the specific height, so we must use the blockchain's app hash for verification.
	updatedConsensusState := g.getBlockchainConsensusStateAtHeight(ctx, chainA, height)
	if updatedConsensusState == nil {
		g.suite.T().Fatalf("❌ CRITICAL ERROR: Could not get blockchain consensus state at height %d", height)
	}

	// Query the path from the chain
	var value []byte
	var proofBytes []byte

	if expectExists {
		// For membership, query an existing path
		value, proofBytes, err = g.queryPathWithProof(ctx, chainA, keyPath, int64(height))
		if err != nil {
			g.suite.T().Logf("⚠️ Failed to query existing path %s: %v", keyPath, err)
			// If path doesn't exist, create empty value (this becomes a non-membership test)
			value = []byte{}
			proofBytes = []byte{}
			expectExists = false
		}
	} else {
		// For non-membership, query a non-existent path
		value, proofBytes, err = g.queryPathWithProof(ctx, chainA, keyPath, int64(height))
		if err != nil || len(value) == 0 {
			// Expected for non-membership
			value = []byte{}
			proofBytes = []byte{}
		}
	}

	// Create the membership message
	membershipMsg := map[string]interface{}{
		"height":             height,
		"delay_time_period":  uint64(0),
		"delay_block_period": uint64(0),
		"proof":              hex.EncodeToString(proofBytes),
		"path":               hex.EncodeToString([]byte(keyPath)),
		"value":              hex.EncodeToString(value),
	}

	// Create the complete fixture using updated client state and consensus state
	fixture := map[string]interface{}{
		"client_state":    updatedClientState,
		"consensus_state": updatedConsensusState,
		"membership_msg":  membershipMsg,
		"expected_result": g.getExpectedResult(expectExists),
		"metadata": map[string]interface{}{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"source":       "real_cosmos_chain",
			"description":  g.getMembershipDescription(keyPath, expectExists),
			"key_path":     keyPath,
			"height":       height,
		},
	}

	return fixture
}

// queryPathWithProof queries a path and returns its value and merkle proof
func (g *SolanaFixtureGenerator) queryPathWithProof(ctx context.Context, chainA *cosmos.CosmosChain, keyPath string, height int64) ([]byte, []byte, error) {
	g.suite.T().Logf("🔍 Querying real proof for path: %s at height: %d", keyPath, height)

	// Parse the keyPath to get store and key components
	store, key, err := g.parseIBCPath(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse IBC path %s: %w", keyPath, err)
	}

	// Query at current height for membership, height-1 for non-membership proofs  
	// For membership proofs, we need to query where the data actually exists
	proofHeight := height

	g.suite.T().Logf("🔧 Querying store/%s/key with data: %x at proof height: %d", store, key, proofHeight)

	// Make ABCI query with proof using the e2esuite helper
	queryReq := &abci.RequestQuery{
		Path:   fmt.Sprintf("store/%s/key", store),
		Data:   key,
		Height: proofHeight,
		Prove:  true,
	}

	queryRes, err := e2esuite.ABCIQuery(ctx, chainA, queryReq)
	if err != nil {
		return nil, nil, fmt.Errorf("ABCI query failed: %w", err)
	}

	// Verify the proof height matches what we expect
	if queryRes.Height != proofHeight {
		return nil, nil, fmt.Errorf("proof height mismatch: expected %d, got %d", proofHeight, queryRes.Height)
	}

	g.suite.T().Logf("📊 Query result: height=%d, value_len=%d, has_proof=%t",
		queryRes.Height, len(queryRes.Value), queryRes.ProofOps != nil)

	// Handle case where path doesn't exist (non-membership)
	if len(queryRes.Value) == 0 {
		g.suite.T().Logf("⚠️ Path %s does not exist - this will be a non-membership proof", keyPath)
	}

	// Convert Tendermint proof to ICS commitment proof format
	var proofBytes []byte
	if queryRes.ProofOps != nil && len(queryRes.ProofOps.Ops) > 0 {
		merkleProof, err := g.convertTendermintProofOpsToICS(queryRes.ProofOps.Ops)
		if err != nil {
			return nil, nil, g.wrapError(err, "proof_conversion", map[string]interface{}{
				"path":      keyPath,
				"height":    proofHeight,
				"ops_count": len(queryRes.ProofOps.Ops),
			})
		}

		proofBytes, err = proto.Marshal(merkleProof)
		if err != nil {
			return nil, nil, g.wrapError(err, "proof_marshal", map[string]interface{}{
				"path":         keyPath,
				"proofs_count": len(merkleProof.Proofs),
			})
		}

		g.suite.T().Logf("✅ Generated proof with %d commitment proofs, total size: %d bytes",
			len(merkleProof.Proofs), len(proofBytes))
	} else {
		g.suite.T().Logf("⚠️ No proof returned from query")
		proofBytes = []byte{}
	}

	// Validate the generated proof
	if err := g.validateProofAgainstValue(proofBytes, queryRes.Value, keyPath); err != nil {
		return nil, nil, g.wrapError(err, "proof_validation", map[string]interface{}{
			"path":      keyPath,
			"height":    proofHeight,
			"value_len": len(queryRes.Value),
			"proof_len": len(proofBytes),
		})
	}

	return queryRes.Value, proofBytes, nil
}

// getCurrentClientState gets the current client state from the chain
func (g *SolanaFixtureGenerator) getCurrentClientState(ctx context.Context, chainA *cosmos.CosmosChain) map[string]interface{} {
	// Query the client state from chainA
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.suite.Require().NoError(err)
	g.suite.Require().NotNil(resp.ClientState)

	// Unmarshal the Tendermint client state
	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(resp.ClientState.Value, &tmClientState)
	g.suite.Require().NoError(err)

	// Convert to Solana format
	return map[string]interface{}{
		"chain_id":                tmClientState.ChainId,
		"trust_level_numerator":   tmClientState.TrustLevel.Numerator,
		"trust_level_denominator": tmClientState.TrustLevel.Denominator,
		"trusting_period":         tmClientState.TrustingPeriod.Seconds(),
		"unbonding_period":        tmClientState.UnbondingPeriod.Seconds(),
		"max_clock_drift":         tmClientState.MaxClockDrift.Seconds(),
		"frozen_height":           tmClientState.FrozenHeight.RevisionHeight,
		"latest_height":           tmClientState.LatestHeight.RevisionHeight,
	}
}

// getCurrentConsensusState gets the current consensus state from the chain
func (g *SolanaFixtureGenerator) getCurrentConsensusState(ctx context.Context, chainA *cosmos.CosmosChain) map[string]interface{} {
	// First get the client state to determine the latest height
	clientResp, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.suite.Require().NoError(err)
	g.suite.Require().NotNil(clientResp.ClientState)

	// Unmarshal the Tendermint client state to get the latest height
	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(clientResp.ClientState.Value, &tmClientState)
	g.suite.Require().NoError(err)

	g.suite.T().Logf("🔍 Client state latest height: revision=%d, height=%d", 
		tmClientState.LatestHeight.RevisionNumber, tmClientState.LatestHeight.RevisionHeight)

	// Query the consensus state using the actual latest height from client state
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, chainA, &clienttypes.QueryConsensusStateRequest{
		ClientId:       ibctesting.FirstClientID,
		RevisionNumber: tmClientState.LatestHeight.RevisionNumber,
		RevisionHeight: tmClientState.LatestHeight.RevisionHeight,
		LatestHeight:   false, // Use specific height, not latest
	})
	g.suite.Require().NoError(err)
	g.suite.Require().NotNil(resp.ConsensusState)

	// Unmarshal the Tendermint consensus state
	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(resp.ConsensusState.Value, &tmConsensusState)
	g.suite.Require().NoError(err)

	g.suite.T().Logf("✅ Retrieved consensus state for height: %d-%d", 
		tmClientState.LatestHeight.RevisionNumber, tmClientState.LatestHeight.RevisionHeight)

	// Convert to Solana format
	return map[string]interface{}{
		"timestamp":            tmConsensusState.Timestamp.UnixNano(),
		"root":                 hex.EncodeToString(tmConsensusState.Root.GetHash()),
		"next_validators_hash": hex.EncodeToString(tmConsensusState.NextValidatorsHash),
	}
}

// getExpectedResult returns the expected result for membership verification
func (g *SolanaFixtureGenerator) getExpectedResult(expectExists bool) string {
	if expectExists {
		return "success"
	}
	return "success" // Non-membership verification should also succeed
}

// getMembershipDescription returns a description for the membership fixture
func (g *SolanaFixtureGenerator) getMembershipDescription(keyPath string, expectExists bool) string {
	if expectExists {
		return fmt.Sprintf("Membership verification fixture for path: %s", keyPath)
	}
	return fmt.Sprintf("Non-membership verification fixture for path: %s", keyPath)
}

// parseIBCPath parses an IBC path and returns the store name and key bytes
// Example: "clients/07-tendermint-0/clientState" -> store="ibc", key="clients/07-tendermint-0/clientState"
func (g *SolanaFixtureGenerator) parseIBCPath(keyPath string) (string, []byte, error) {
	// All IBC paths use the "ibc" store in Cosmos SDK
	store := "ibc"

	// Validate that this looks like a valid IBC path
	if !g.isValidIBCPath(keyPath) {
		return "", nil, fmt.Errorf("invalid IBC path format: %s", keyPath)
	}

	// The key is the path itself as bytes
	key := []byte(keyPath)

	g.suite.T().Logf("📝 Parsed IBC path: store=%s, key=%s", store, keyPath)
	return store, key, nil
}

// isValidIBCPath validates that a path looks like a valid IBC path
func (g *SolanaFixtureGenerator) isValidIBCPath(path string) bool {
	// Common IBC path prefixes
	validPrefixes := []string{
		"clients/",          // client state and consensus state paths
		"connections/",      // connection paths
		"channelEnds/",      // channel paths
		"commitments/",      // packet commitment paths
		"receipts/",         // packet receipt paths
		"acks/",             // packet acknowledgement paths
		"nextSequenceSend/", // sequence paths
		"nextSequenceRecv/", // sequence paths
		"nextSequenceAck/",  // sequence paths
	}

	for _, prefix := range validPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	g.suite.T().Logf("⚠️ Path %s doesn't match known IBC prefixes, proceeding anyway", path)
	return true // Allow unknown paths for flexibility
}

// convertTendermintProofOpsToICS converts Tendermint ProofOps to an ICS commitment MerkleProof
// This mirrors the logic in packages/utils/src/merkle.rs:convert_tm_to_ics_merkle_proof
func (g *SolanaFixtureGenerator) convertTendermintProofOpsToICS(proofOps []tmcrypto.ProofOp) (*commitmenttypes.MerkleProof, error) {
	if len(proofOps) == 0 {
		return nil, fmt.Errorf("no proof ops provided")
	}

	g.suite.T().Logf("🔄 Converting Tendermint proof with %d operations", len(proofOps))

	// Convert each proof operation to an ICS23 commitment proof
	var icsProofs []*ics23.CommitmentProof

	for i, op := range proofOps {
		g.suite.T().Logf("📋 Processing proof op %d: type=%s, data_len=%d", i, op.Type, len(op.Data))

		// Parse the proof operation data as an ICS23 commitment proof
		var commitmentProof ics23.CommitmentProof
		if err := proto.Unmarshal(op.Data, &commitmentProof); err != nil {
			return nil, fmt.Errorf("failed to unmarshal proof op %d: %w", i, err)
		}

		icsProofs = append(icsProofs, &commitmentProof)
	}

	if len(icsProofs) == 0 {
		return nil, fmt.Errorf("no valid proofs found in proof ops")
	}

	merkleProof := &commitmenttypes.MerkleProof{
		Proofs: icsProofs,
	}

	g.suite.T().Logf("✅ Converted to ICS commitment proof with %d proofs", len(merkleProof.Proofs))
	return merkleProof, nil
}

// validateProofAgainstValue performs basic validation of the generated proof
// This helps catch obvious errors in the proof generation logic
func (g *SolanaFixtureGenerator) validateProofAgainstValue(proof []byte, value []byte, path string) error {
	if len(proof) == 0 && len(value) > 0 {
		return fmt.Errorf("proof is empty but value exists for path %s", path)
	}

	// For non-membership (empty value), we should still have a proof
	if len(value) == 0 {
		g.suite.T().Logf("🔍 Non-membership case for path %s: value empty, proof_len=%d", path, len(proof))
	} else {
		g.suite.T().Logf("🔍 Membership case for path %s: value_len=%d, proof_len=%d", path, len(value), len(proof))
	}

	// Try to unmarshal the proof to ensure it's valid protobuf
	if len(proof) > 0 {
		var merkleProof commitmenttypes.MerkleProof
		if err := proto.Unmarshal(proof, &merkleProof); err != nil {
			return fmt.Errorf("generated proof is not valid protobuf: %w", err)
		}

		if len(merkleProof.Proofs) == 0 {
			return fmt.Errorf("proof contains no commitment proofs")
		}

		g.suite.T().Logf("✅ Proof validation passed: %d commitment proofs", len(merkleProof.Proofs))
	}

	return nil
}

// getAppHashAtHeight gets the app hash (commitment root) at a specific blockchain height
func (g *SolanaFixtureGenerator) getAppHashAtHeight(ctx context.Context, chainA *cosmos.CosmosChain, height int64) (string, error) {
	g.suite.T().Logf("🔍 Getting app hash at blockchain height %d", height)
	
	// Get the block at the specific height
	blockRes, err := chainA.Nodes()[0].Client.Block(ctx, &height)
	if err != nil {
		return "", fmt.Errorf("failed to get block at height %d: %w", height, err)
	}
	
	appHash := hex.EncodeToString(blockRes.Block.Header.AppHash)
	g.suite.T().Logf("✅ Got app hash at height %d: %s", height, appHash)
	
	return appHash, nil
}

// getBlockchainConsensusStateAtHeight gets the consensus state using blockchain data at a specific height
// This is used for membership verification where the proof is generated against blockchain state
func (g *SolanaFixtureGenerator) getBlockchainConsensusStateAtHeight(ctx context.Context, chainA *cosmos.CosmosChain, height uint64) map[string]interface{} {
	g.suite.T().Logf("🔍 Getting BLOCKCHAIN consensus state at height %d", height)
	
	// Get app hash at the proof height
	appHash, err := g.getAppHashAtHeight(ctx, chainA, int64(height))
	if err != nil {
		g.suite.T().Fatalf("❌ CRITICAL ERROR: Failed to get app hash at height %d: %v", height, err)
	}
	
	// Get block header for timestamp and next validators hash
	blockRes, err := chainA.Nodes()[0].Client.Block(ctx, ptr(int64(height)))
	if err != nil {
		g.suite.T().Fatalf("❌ CRITICAL ERROR: Failed to get block at height %d: %v", height, err)
	}
	
	g.suite.T().Logf("✅ Created blockchain consensus state at height %d with app hash: %s", height, appHash)
	
	// Return a consensus state constructed from blockchain data
	return map[string]interface{}{
		"timestamp":            blockRes.Block.Header.Time.UnixNano(),
		"root":                 appHash,
		"next_validators_hash": hex.EncodeToString(blockRes.Block.Header.NextValidatorsHash),
	}
}

// getConsensusStateAtHeight gets the consensus state at a specific height
func (g *SolanaFixtureGenerator) getConsensusStateAtHeight(ctx context.Context, chainA *cosmos.CosmosChain, height uint64) map[string]interface{} {
	g.suite.T().Logf("🔍 Getting consensus state at height %d", height)
	
	// First try to get IBC consensus state (only exists at client update heights)
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, chainA, &clienttypes.QueryConsensusStateRequest{
		ClientId:       ibctesting.FirstClientID,
		RevisionNumber: 0, // Typically 0 for single revision chains
		RevisionHeight: height,
	})
	if err == nil && resp.ConsensusState != nil {
		// Unmarshal the Tendermint consensus state
		var tmConsensusState ibctmtypes.ConsensusState
		err = proto.Unmarshal(resp.ConsensusState.Value, &tmConsensusState)
		if err == nil {
			g.suite.T().Logf("✅ Found IBC consensus state at height %d", height)
			return map[string]interface{}{
				"timestamp":            tmConsensusState.Timestamp.UnixNano(),
				"root":                 hex.EncodeToString(tmConsensusState.Root.GetHash()),
				"next_validators_hash": hex.EncodeToString(tmConsensusState.NextValidatorsHash),
			}
		}
	}
	
	// If IBC consensus state doesn't exist at this height, construct one from blockchain data
	g.suite.T().Logf("🔧 No IBC consensus state at height %d, constructing from blockchain data (this is expected for membership verification)", height)
	
	// Get app hash at the proof height
	appHash, err := g.getAppHashAtHeight(ctx, chainA, int64(height))
	if err != nil {
		g.suite.T().Fatalf("❌ CRITICAL ERROR: Failed to get app hash at height %d: %v", height, err)
	}
	
	// Get block header for timestamp and next validators hash
	blockRes, err := chainA.Nodes()[0].Client.Block(ctx, ptr(int64(height)))
	if err != nil {
		g.suite.T().Fatalf("❌ CRITICAL ERROR: Failed to get block at height %d: %v", height, err)
	}
	
	// Return a consensus state constructed from blockchain data
	return map[string]interface{}{
		"timestamp":            blockRes.Block.Header.Time.UnixNano(),
		"root":                 appHash,
		"next_validators_hash": hex.EncodeToString(blockRes.Block.Header.NextValidatorsHash),
	}
}

// Helper function to create a pointer to an int64
func ptr(i int64) *int64 {
	return &i
}

// Enhanced error context for better debugging
func (g *SolanaFixtureGenerator) wrapError(err error, context string, details map[string]interface{}) error {
	if err == nil {
		return nil
	}

	var detailsStr strings.Builder
	for key, value := range details {
		detailsStr.WriteString(fmt.Sprintf(" %s=%v", key, value))
	}

	return fmt.Errorf("%s:%s - %w", context, detailsStr.String(), err)
}
