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

	"github.com/cosmos/cosmos-sdk/codec/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	ibctmtypes "github.com/cosmos/ibc-go/v10/modules/light-clients/07-tendermint"
	ibctesting "github.com/cosmos/ibc-go/v10/testing"

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
