package tendermint_light_client_fixtures

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/cosmos/gogoproto/proto"
	ics23 "github.com/cosmos/ics23/go"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"

	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types"
	ibcexported "github.com/cosmos/ibc-go/v10/modules/core/exported"
	ibctmtypes "github.com/cosmos/ibc-go/v10/modules/light-clients/07-tendermint"
	ibctesting "github.com/cosmos/ibc-go/v10/testing"

	"github.com/strangelove-ventures/interchaintest/v8/chain/cosmos"

	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/e2esuite"
)

// KeyPath represents a key path with its expected membership status
type KeyPath struct {
	Key        string
	Membership bool
}

// MembershipFixtureGenerator handles generation of membership verification test scenarios
type MembershipFixtureGenerator struct {
	generator FixtureGeneratorInterface
}

// NewMembershipFixtureGenerator creates a new membership fixture generator
func NewMembershipFixtureGenerator(generator FixtureGeneratorInterface) *MembershipFixtureGenerator {
	return &MembershipFixtureGenerator{
		generator: generator,
	}
}

// GenerateMembershipVerificationScenariosWithPredefinedKeys generates membership fixtures using predefined keys
func (g *MembershipFixtureGenerator) GenerateMembershipVerificationScenariosWithPredefinedKeys(ctx context.Context, chainA *cosmos.CosmosChain, keyPaths []KeyPath) {
	if !g.generator.IsEnabled() {
		return
	}
	g.generator.LogInfo("🔧 Generating membership verification scenarios with predefined keys")

	for i, keySpec := range keyPaths {
		membershipType := "membership"
		if !keySpec.Membership {
			membershipType = "non-membership"
		}
		g.generator.LogInfof("🔍 Processing predefined key path: %s (%s)", keySpec.Key, membershipType)
		g.generateMembershipFixtureForKey(ctx, chainA, keySpec.Key, i, keySpec.Membership)
	}

	g.generator.LogInfo("✅ Predefined key membership scenarios generated successfully")
}

// generateMembershipFixtureForKey generates a membership fixture for a specific predefined key
func (g *MembershipFixtureGenerator) generateMembershipFixtureForKey(ctx context.Context, chainA *cosmos.CosmosChain, keyPath string, index int, expectMembership bool) {
	proofType := "membership"
	if !expectMembership {
		proofType = "non-membership"
	}
	g.generator.LogInfof("🔧 Generating %s fixture for key: %s", proofType, keyPath)

	// Get the current chain height for the query
	clientState, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.generator.RequireNoError(err)

	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(clientState.ClientState.Value, &tmClientState)
	g.generator.RequireNoError(err)
	currentHeight := tmClientState.LatestHeight.RevisionHeight

	// Query using ABCI with the predefined key path
	abciReq := &abci.RequestQuery{
		Path:   "store/" + string(ibcexported.StoreKey) + "/key",
		Data:   []byte(keyPath),
		Height: int64(currentHeight) - 1, // Use height-1 for proof generation
		Prove:  true,
	}

	g.generator.LogInfof("📡 ABCI Query: path=store/%s/key, data=%s, height=%d, prove=true", string(ibcexported.StoreKey), keyPath, abciReq.Height)

	abciResp, err := e2esuite.ABCIQuery(ctx, chainA, abciReq)
	g.generator.RequireNoError(err)

	// Verify that the actual membership status matches our expectation
	isMembership := len(abciResp.Value) > 0
	if expectMembership && !isMembership {
		g.generator.Fatalf("❌ Expected membership proof for key %s but value is empty", keyPath)
	}
	if !expectMembership && isMembership {
		g.generator.Fatalf("❌ Expected non-membership proof for key %s but value exists (length: %d)", keyPath, len(abciResp.Value))
	}
	
	if isMembership {
		g.generator.LogInfof("✅ ABCI query successful - MEMBERSHIP case (as expected): value length: %d, proof ops: %d", len(abciResp.Value), len(abciResp.ProofOps.Ops))
	} else {
		g.generator.LogInfof("✅ ABCI query successful - NON-MEMBERSHIP case (as expected): empty value, proof ops: %d", len(abciResp.ProofOps.Ops))
	}

	if len(abciResp.ProofOps.Ops) == 0 {
		g.generator.LogInfof("⚠️  ABCI proof is empty for key: %s, skipping", keyPath)
		return
	}

	// TODO: Convert ABCI proof operations to IBC MerkleProof format
	// For now, use the original approach but add detailed logging to understand the structure
	g.generator.LogInfof("🔍 ABCI ProofOps analysis: %d operations", len(abciResp.ProofOps.Ops))
	for i, op := range abciResp.ProofOps.Ops {
		g.generator.LogInfof("   ProofOp[%d]: type=%s, key_len=%d, data_len=%d", i, op.Type, len(op.Key), len(op.Data))
		// Try to understand what's in op.Data
		if len(op.Data) > 0 {
			g.generator.LogInfof("   ProofOp[%d] data first 32 bytes: %x", i, op.Data[:min(32, len(op.Data))])
		}
	}

	// Convert ABCI ProofOps to IBC MerkleProof format
	proofBytes, err := g.convertABCIProofOpsToMerkleProof(abciResp.ProofOps)
	g.generator.RequireNoError(err)
	g.generator.LogInfof("📦 Converted ABCI ProofOps to IBC MerkleProof: %d bytes", len(proofBytes))

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
		g.generator.LogInfof("⚠️  No consensus state at proof height %d, finding closest available", proofHeight)

		// Query all consensus states to find the best match
		allConsensusStatesResp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStatesResponse](ctx, chainA, &clienttypes.QueryConsensusStatesRequest{
			ClientId: ibctesting.FirstClientID,
		})
		g.generator.RequireNoError(err)
		g.generator.RequireNotNil(allConsensusStatesResp.ConsensusStates, "No consensus states found for client")

		// Find the consensus state with height closest to but not exceeding proof height
		var bestMatch *clienttypes.ConsensusStateWithHeight
		for _, cs := range allConsensusStatesResp.ConsensusStates {
			if cs.Height.RevisionHeight <= proofHeight {
				if bestMatch == nil || cs.Height.RevisionHeight > bestMatch.Height.RevisionHeight {
					bestMatch = &cs
				}
			}
		}

		g.generator.RequireNotNil(bestMatch, "No suitable consensus state found")
		actualHeight := bestMatch.Height.RevisionHeight
		g.generator.LogInfof("🔍 Using consensus state at height %d (closest to proof height %d)", actualHeight, proofHeight)

		// Now query ABCI again with the consensus state height to get matching proof
		abciReq = &abci.RequestQuery{
			Path:   "store/" + string(ibcexported.StoreKey) + "/key",
			Data:   []byte(keyPath),
			Height: int64(actualHeight) - 1, // Use consensus state height for proof
			Prove:  true,
		}

		g.generator.LogInfof("📡 Re-querying ABCI with consensus state height: path=store/%s/key, data=%s, height=%d, prove=true",
			string(ibcexported.StoreKey), keyPath, abciReq.Height)

		abciResp, err = e2esuite.ABCIQuery(ctx, chainA, abciReq)
		g.generator.RequireNoError(err)
		// For non-membership proofs, the value will be empty, which is expected
		g.generator.RequireNotNil(abciResp.ProofOps.Ops, "ABCI proof is empty after re-query")

		// Re-convert the proof with the new query results
		proofBytes, err = g.convertABCIProofOpsToMerkleProof(abciResp.ProofOps)
		g.generator.RequireNoError(err)
		g.generator.LogInfof("📦 Re-converted ABCI ProofOps to IBC MerkleProof: %d bytes", len(proofBytes))

		// Re-check membership status after re-query and verify it matches expectations
		isMembership = len(abciResp.Value) > 0
		if expectMembership && !isMembership {
			g.generator.Fatalf("❌ After re-query: Expected membership proof for key %s but value is empty", keyPath)
		}
		if !expectMembership && isMembership {
			g.generator.Fatalf("❌ After re-query: Expected non-membership proof for key %s but value exists (length: %d)", keyPath, len(abciResp.Value))
		}
		g.generator.LogInfof("✅ Re-query successful - membership status verified as expected (%s)", proofType)

		// Update proof height to match consensus state
		proofHeight = actualHeight

		// Use the best match consensus state
		var tmConsensusState ibctmtypes.ConsensusState
		err = proto.Unmarshal(bestMatch.ConsensusState.Value, &tmConsensusState)
		g.generator.RequireNoError(err)

		// Store consensus state for later use
		consensusStateResp = &clienttypes.QueryConsensusStateResponse{
			ConsensusState: bestMatch.ConsensusState,
			ProofHeight:    bestMatch.Height,
		}
	}

	// Extract consensus state
	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(consensusStateResp.ConsensusState.Value, &tmConsensusState)
	g.generator.RequireNoError(err)

	// Create the membership/non-membership proof message
	description := fmt.Sprintf("Valid %s proof for predefined key: %s", proofType, keyPath)

	membershipMsg := map[string]interface{}{
		"height":             proofHeight,
		"delay_time_period":  0,
		"delay_block_period": 0,
		"proof":              hex.EncodeToString(proofBytes),
		"path":               []string{string(ibcexported.StoreKey), keyPath}, // Include IBC store prefix like SP1
		"value":              hex.EncodeToString(abciResp.Value),
		"metadata":           g.generator.CreateMetadata(description),
	}

	// Get client state for context
	tmClientStatePtr := g.generator.QueryTendermintClientState(ctx, chainA)
	clientStateMap := g.generator.ConvertClientStateToFixtureFormat(tmClientStatePtr, chainA.Config().ChainID)

	consensusStateMap := map[string]interface{}{
		"timestamp":            tmConsensusState.Timestamp.UnixNano(),
		"root":                 hex.EncodeToString(tmConsensusState.Root.GetHash()),
		"next_validators_hash": hex.EncodeToString(tmConsensusState.NextValidatorsHash),
		"metadata":             g.generator.CreateMetadata(fmt.Sprintf("Consensus state at height %d", proofHeight)),
	}

	scenarioName := fmt.Sprintf("%s_predefined_key_%d", proofType, index)
	unifiedFixture := map[string]interface{}{
		"scenario":        scenarioName,
		"client_state":    clientStateMap,
		"consensus_state": consensusStateMap,
		"membership_msg":  membershipMsg,
		"key_info": map[string]interface{}{
			"path":        keyPath,
			"value_size":  len(abciResp.Value),
			"description": fmt.Sprintf("Predefined IBC key: %s (%s)", keyPath, proofType),
			"proof_type":  proofType,
		},
		"metadata": g.generator.CreateUnifiedMetadata(scenarioName, chainA.Config().ChainID),
	}

	filename := filepath.Join(g.generator.GetFixtureDir(), fmt.Sprintf("verify_%s_predefined_key_%d.json", proofType, index))
	g.generator.SaveJsonFixture(filename, unifiedFixture)
	g.generator.LogInfof("💾 Predefined key %s fixture saved: %s", proofType, filename)
}

// convertABCIProofOpsToMerkleProof converts ABCI ProofOps format to IBC MerkleProof format
func (g *MembershipFixtureGenerator) convertABCIProofOpsToMerkleProof(proofOps *cmtcrypto.ProofOps) ([]byte, error) {
	g.generator.LogInfof("🔄 Converting %d ABCI ProofOps to IBC MerkleProof format", len(proofOps.Ops))

	// Each ProofOp contains ICS23 CommitmentProof data in op.Data
	// We need to extract these and create an IBC MerkleProof
	var commitmentProofs []*ics23.CommitmentProof

	for i, op := range proofOps.Ops {
		g.generator.LogInfof("   Processing ProofOp[%d]: type=%s, key_len=%d, data_len=%d",
			i, op.Type, len(op.Key), len(op.Data))

		// The op.Data contains the ICS23 CommitmentProof
		// Parse it as a CommitmentProof
		var commitmentProof ics23.CommitmentProof
		if err := proto.Unmarshal(op.Data, &commitmentProof); err != nil {
			g.generator.LogInfof("   ❌ Failed to unmarshal CommitmentProof from ProofOp[%d]: %v", i, err)
			return nil, fmt.Errorf("failed to unmarshal CommitmentProof from ProofOp[%d]: %w", i, err)
		}

		g.generator.LogInfof("   ✅ Successfully parsed CommitmentProof from ProofOp[%d]", i)
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

	g.generator.LogInfof("✅ Successfully converted ABCI ProofOps to IBC MerkleProof: %d bytes", len(proofBytes))
	return proofBytes, nil
}
