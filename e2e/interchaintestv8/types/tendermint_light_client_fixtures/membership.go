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
	channeltypesv2 "github.com/cosmos/ibc-go/v10/modules/core/04-channel/v2/types"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types"
	ibcexported "github.com/cosmos/ibc-go/v10/modules/core/exported"
	ibctmtypes "github.com/cosmos/ibc-go/v10/modules/light-clients/07-tendermint"
	ibctesting "github.com/cosmos/ibc-go/v10/testing"

	"github.com/strangelove-ventures/interchaintest/v8/chain/cosmos"

	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/e2esuite"
)

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

// GenerateMembershipVerificationScenarios generates fixtures for membership verification tests (happy path only)
func (g *MembershipFixtureGenerator) GenerateMembershipVerificationScenarios(ctx context.Context, chainA *cosmos.CosmosChain, packet channeltypesv2.Packet) {
	if !g.generator.IsEnabled() {
		return
	}

	g.generator.LogInfo("🔧 Generating membership verification scenario (happy path only)")

	// Generate happy path scenario with real packet commitment
	g.generateMembershipHappyPath(ctx, chainA, packet)

	g.generator.LogInfo("✅ Membership verification scenario generated successfully")
}

// GenerateMembershipVerificationScenariosWithPredefinedKeys generates membership fixtures using predefined keys
func (g *MembershipFixtureGenerator) GenerateMembershipVerificationScenariosWithPredefinedKeys(ctx context.Context, chainA *cosmos.CosmosChain, keyPaths []string) {
	if !g.generator.IsEnabled() {
		return
	}
	g.generator.LogInfo("🔧 Generating membership verification scenarios with predefined keys")

	for i, keyPath := range keyPaths {
		g.generator.LogInfof("🔍 Processing predefined key path: %s", keyPath)
		g.generateMembershipFixtureForKey(ctx, chainA, keyPath, i)
	}

	g.generator.LogInfo("✅ Predefined key membership scenarios generated successfully")
}

// generateMembershipFixtureForKey generates a membership fixture for a specific predefined key
func (g *MembershipFixtureGenerator) generateMembershipFixtureForKey(ctx context.Context, chainA *cosmos.CosmosChain, keyPath string, index int) {
	g.generator.LogInfof("🔧 Generating membership fixture for key: %s", keyPath)

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

	if len(abciResp.Value) == 0 {
		g.generator.LogInfof("⚠️  ABCI value is empty for key: %s, skipping", keyPath)
		return
	}

	if len(abciResp.ProofOps.Ops) == 0 {
		g.generator.LogInfof("⚠️  ABCI proof is empty for key: %s, skipping", keyPath)
		return
	}

	g.generator.LogInfof("✅ ABCI query successful - value length: %d, proof ops: %d", len(abciResp.Value), len(abciResp.ProofOps.Ops))

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
		g.generator.RequireNotNil(abciResp.Value, "ABCI value is empty after re-query")
		g.generator.RequireNotNil(abciResp.ProofOps.Ops, "ABCI proof is empty after re-query")

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

	// Create the membership proof message
	membershipMsg := map[string]interface{}{
		"height":             proofHeight,
		"delay_time_period":  0,
		"delay_block_period": 0,
		"proof":              hex.EncodeToString(proofBytes),
		"path":               []string{string(ibcexported.StoreKey), keyPath}, // Include IBC store prefix like SP1
		"value":              hex.EncodeToString(abciResp.Value),
		"metadata":           g.generator.CreateMetadata(fmt.Sprintf("Valid membership proof for predefined key: %s", keyPath)),
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

	unifiedFixture := map[string]interface{}{
		"scenario":        fmt.Sprintf("membership_predefined_key_%d", index),
		"client_state":    clientStateMap,
		"consensus_state": consensusStateMap,
		"membership_msg":  membershipMsg,
		"key_info": map[string]interface{}{
			"path":        keyPath,
			"value_size":  len(abciResp.Value),
			"description": fmt.Sprintf("Predefined IBC key: %s", keyPath),
		},
		"metadata": g.generator.CreateUnifiedMetadata(fmt.Sprintf("membership_predefined_key_%d", index), chainA.Config().ChainID),
	}

	filename := filepath.Join(g.generator.GetFixtureDir(), fmt.Sprintf("verify_membership_predefined_key_%d.json", index))
	g.generator.SaveJsonFixture(filename, unifiedFixture)
	g.generator.LogInfof("💾 Predefined key membership fixture saved: %s", filename)
}

// generateMembershipHappyPath generates a valid membership proof for a real packet
func (g *MembershipFixtureGenerator) generateMembershipHappyPath(ctx context.Context, chainA *cosmos.CosmosChain, packet channeltypesv2.Packet) {
	g.generator.LogInfo("🔧 Generating membership happy path scenario")

	// Get the current chain height for the query
	clientState, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, chainA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	g.generator.RequireNoError(err)

	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(clientState.ClientState.Value, &tmClientState)
	g.generator.RequireNoError(err)
	currentHeight := tmClientState.LatestHeight.RevisionHeight

	// Query the packet commitment with proof using ABCI
	g.generator.LogInfof("🔍 Querying packet commitment via ABCI for ClientId: %s, Sequence: %d, Height: %d", packet.SourceClient, packet.Sequence, currentHeight)
	abciResp, err := g.queryPacketCommitmentWithProof(ctx, chainA, packet.SourceClient, packet.Sequence, currentHeight)
	g.generator.RequireNoError(err)

	if len(abciResp.Value) == 0 {
		g.generator.LogInfof("❌ ABCI commitment value is empty for ClientId: %s, Sequence: %d", packet.SourceClient, packet.Sequence)
	} else {
		g.generator.LogInfof("✅ ABCI commitment found: %x", abciResp.Value)
	}

	if len(abciResp.ProofOps.Ops) == 0 {
		g.generator.LogInfof("❌ ABCI proof is empty for ClientId: %s, Sequence: %d", packet.SourceClient, packet.Sequence)
	} else {
		g.generator.LogInfof("✅ ABCI proof found with %d operations", len(abciResp.ProofOps.Ops))
	}

	g.generator.RequireNotNil(abciResp.Value, "Packet commitment value should not be empty")
	g.generator.RequireNotNil(abciResp.ProofOps.Ops, "Merkle proof should not be empty")
	g.generator.RequireGreater(abciResp.Height, int64(0), "Proof height should not be zero")

	// Get consensus state at proof height
	proofHeight := uint64(abciResp.Height + 1) // ABCI returns height-1, so add 1 for actual height
	consensusStateResp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, chainA, &clienttypes.QueryConsensusStateRequest{
		ClientId:       ibctesting.FirstClientID,
		RevisionNumber: 0, // Use revision number 0 for simd chains
		RevisionHeight: proofHeight,
	})
	g.generator.RequireNoError(err)

	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(consensusStateResp.ConsensusState.Value, &tmConsensusState)
	g.generator.RequireNoError(err)

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
		"metadata":           g.generator.CreateMetadata("Valid membership proof for packet commitment"),
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

	unifiedFixture := map[string]interface{}{
		"scenario":        "membership_happy_path",
		"client_state":    clientStateMap,
		"consensus_state": consensusStateMap,
		"membership_msg":  membershipMsg,
		"packet_info": map[string]interface{}{
			"sequence":         packet.Sequence,
			"source_port":      sourcePort,
			"destination_port": destPort,
		},
		"metadata": g.generator.CreateUnifiedMetadata("membership_happy_path", tmClientState.ChainId),
	}

	filename := filepath.Join(g.generator.GetFixtureDir(), "verify_membership_happy_path.json")
	g.generator.SaveJsonFixture(filename, unifiedFixture)
	g.generator.LogInfof("💾 Membership happy path fixture saved: %s", filename)
}

// Helper function to construct ICS24 commitment path
func (g *MembershipFixtureGenerator) constructCommitmentPath(sequence uint64, sourceChannel, destChannel string) []string {
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
func (g *MembershipFixtureGenerator) queryPacketCommitmentWithProof(ctx context.Context, chain *cosmos.CosmosChain, clientId string, sequence uint64, height uint64) (*abci.ResponseQuery, error) {
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

	g.generator.LogInfof("📡 ABCI Query: path=store/%s/key, data=%s, height=%d, prove=true", string(ibcexported.StoreKey), packetCommitmentPath, abciReq.Height)

	return e2esuite.ABCIQuery(ctx, chain, abciReq)
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
