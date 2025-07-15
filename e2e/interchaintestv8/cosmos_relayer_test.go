package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/suite"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/codec/types"

	transfertypes "github.com/cosmos/ibc-go/v10/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	clienttypesv2 "github.com/cosmos/ibc-go/v10/modules/core/02-client/v2/types"
	channeltypesv2 "github.com/cosmos/ibc-go/v10/modules/core/04-channel/v2/types"
	ibcexported "github.com/cosmos/ibc-go/v10/modules/core/exported"
	ibctmtypes "github.com/cosmos/ibc-go/v10/modules/light-clients/07-tendermint"
	ibctesting "github.com/cosmos/ibc-go/v10/testing"

	"github.com/strangelove-ventures/interchaintest/v8/chain/cosmos"
	"github.com/strangelove-ventures/interchaintest/v8/ibc"

	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/chainconfig"
	cosmosutils "github.com/srdtrk/solidity-ibc-eureka/e2e/v8/cosmos"
	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/e2esuite"
	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/relayer"
	"github.com/srdtrk/solidity-ibc-eureka/e2e/v8/testvalues"
	relayertypes "github.com/srdtrk/solidity-ibc-eureka/e2e/v8/types/relayer"
)

// CosmosRelayerTestSuite is a struct that holds the test suite for two Cosmos chains.
type CosmosRelayerTestSuite struct {
	e2esuite.TestSuite

	SimdA *cosmos.CosmosChain
	SimdB *cosmos.CosmosChain

	SimdASubmitter ibc.Wallet
	SimdBSubmitter ibc.Wallet

	RelayerClient relayertypes.RelayerServiceClient
	
	// Fixture generation
	generateSolanaFixtures bool
	solanaFixtureDir      string
}

// TestWithIbcEurekaTestSuite is the boilerplate code that allows the test suite to be run
func TestWithCosmosRelayerTestSuite(t *testing.T) {
	suite.Run(t, new(CosmosRelayerTestSuite))
}

// SetupSuite calls the underlying IbcEurekaTestSuite's SetupSuite method
// and deploys the IbcEureka contract
func (s *CosmosRelayerTestSuite) SetupSuite(ctx context.Context) {
	chainconfig.DefaultChainSpecs = append(chainconfig.DefaultChainSpecs, chainconfig.IbcGoChainSpec("ibc-go-simd-2", "simd-2"))

	os.Setenv(testvalues.EnvKeyEthTestnetType, testvalues.EthTestnetTypeNone)

	// Initialize fixture generation
	s.generateSolanaFixtures = os.Getenv("GENERATE_SOLANA_FIXTURES") == "true"
	s.solanaFixtureDir = "programs/solana/tests/fixtures/"
	
	if s.generateSolanaFixtures {
		// Create absolute path to avoid issues with directory changes
		absPath, err := filepath.Abs(filepath.Join("..", s.solanaFixtureDir))
		if err != nil {
			s.T().Fatalf("Failed to get absolute path for fixtures: %v", err)
		}
		s.solanaFixtureDir = absPath
		
		if err := os.MkdirAll(s.solanaFixtureDir, 0755); err != nil {
			s.T().Fatalf("Failed to create Solana fixture directory: %v", err)
		}
		s.T().Logf("📁 Solana fixtures will be saved to: %s", s.solanaFixtureDir)
	}

	s.TestSuite.SetupSuite(ctx)

	s.SimdA, s.SimdB = s.CosmosChains[0], s.CosmosChains[1]
	s.SimdASubmitter = s.CreateAndFundCosmosUser(ctx, s.SimdA)
	s.SimdBSubmitter = s.CreateAndFundCosmosUser(ctx, s.SimdB)

	var relayerProcess *os.Process
	s.Require().True(s.Run("Start Relayer", func() {
		err := os.Chdir("../..")
		s.Require().NoError(err)

		config := relayer.NewConfig(
			relayer.CreateCosmosCosmosModules(relayer.CosmosToCosmosConfigInfo{
				ChainAID:    s.SimdA.Config().ChainID,
				ChainBID:    s.SimdB.Config().ChainID,
				ChainATmRPC: s.SimdA.GetHostRPCAddress(),
				ChainBTmRPC: s.SimdB.GetHostRPCAddress(),
				ChainAUser:  s.SimdASubmitter.FormattedAddress(),
				ChainBUser:  s.SimdBSubmitter.FormattedAddress(),
			}),
		)

		err = config.GenerateConfigFile(testvalues.RelayerConfigFilePath)
		s.Require().NoError(err)

		relayerProcess, err = relayer.StartRelayer(testvalues.RelayerConfigFilePath)
		s.Require().NoError(err)

		s.T().Cleanup(func() {
			os.Remove(testvalues.RelayerConfigFilePath)
		})
	}))

	s.T().Cleanup(func() {
		if relayerProcess != nil {
			err := relayerProcess.Kill()
			if err != nil {
				s.T().Logf("Failed to kill the relayer process: %v", err)
			}
		}
	})

	s.Require().True(s.Run("Create Relayer Client", func() {
		var err error
		s.RelayerClient, err = relayer.GetGRPCClient(relayer.DefaultRelayerGRPCAddress())
		s.Require().NoError(err)
	}))

	s.Require().True(s.Run("Create Light Client of Chain A on Chain B", func() {
		var createClientTxBodyBz []byte
		s.Require().True(s.Run("Retrieve create client tx", func() {
			resp, err := s.RelayerClient.CreateClient(context.Background(), &relayertypes.CreateClientRequest{
				SrcChain: s.SimdA.Config().ChainID,
				DstChain: s.SimdB.Config().ChainID,
			})
			s.Require().NoError(err)
			s.Require().NotEmpty(resp.Tx)
			s.Require().Empty(resp.Address)

			createClientTxBodyBz = resp.Tx
		}))

		s.Require().True(s.Run("Broadcast relay tx", func() {
			resp := s.MustBroadcastSdkTxBody(ctx, s.SimdB, s.SimdBSubmitter, 2_000_000, createClientTxBodyBz)
			clientId, err := cosmosutils.GetEventValue(resp.Events, clienttypes.EventTypeCreateClient, clienttypes.AttributeKeyClientID)
			s.Require().NoError(err)
			s.Require().Equal(ibctesting.FirstClientID, clientId)
		}))
	}))

	s.Require().True(s.Run("Create Light Client of Chain B on Chain A", func() {
		var createClientTxBodyBz []byte
		s.Require().True(s.Run("Retrieve create client tx", func() {
			resp, err := s.RelayerClient.CreateClient(context.Background(), &relayertypes.CreateClientRequest{
				SrcChain: s.SimdB.Config().ChainID,
				DstChain: s.SimdA.Config().ChainID,
			})
			s.Require().NoError(err)
			s.Require().NotEmpty(resp.Tx)
			s.Require().Empty(resp.Address)

			createClientTxBodyBz = resp.Tx
		}))

		s.Require().True(s.Run("Broadcast relay tx", func() {
			resp := s.MustBroadcastSdkTxBody(ctx, s.SimdA, s.SimdASubmitter, 2_000_000, createClientTxBodyBz)
			clientId, err := cosmosutils.GetEventValue(resp.Events, clienttypes.EventTypeCreateClient, clienttypes.AttributeKeyClientID)
			s.Require().NoError(err)
			s.Require().Equal(ibctesting.FirstClientID, clientId)
		}))
	}))

	s.Require().True(s.Run("Register counterparty on Chain A", func() {
		merklePathPrefix := [][]byte{[]byte(ibcexported.StoreKey), []byte("")}

		// We can do this because we know what the counterparty client ID will be
		_, err := s.BroadcastMessages(ctx, s.SimdA, s.SimdASubmitter, 200_000, &clienttypesv2.MsgRegisterCounterparty{
			ClientId:                 ibctesting.FirstClientID,
			CounterpartyClientId:     ibctesting.FirstClientID,
			CounterpartyMerklePrefix: merklePathPrefix,
			Signer:                   s.SimdASubmitter.FormattedAddress(),
		})
		s.Require().NoError(err)
	}))

	s.Require().True(s.Run("Register counterparty on Chain B", func() {
		merklePathPrefix := [][]byte{[]byte(ibcexported.StoreKey), []byte("")}

		_, err := s.BroadcastMessages(ctx, s.SimdB, s.SimdBSubmitter, 200_000, &clienttypesv2.MsgRegisterCounterparty{
			ClientId:                 ibctesting.FirstClientID,
			CounterpartyClientId:     ibctesting.FirstClientID,
			CounterpartyMerklePrefix: merklePathPrefix,
			Signer:                   s.SimdBSubmitter.FormattedAddress(),
		})
		s.Require().NoError(err)
	}))
}

// TestRelayer is a test that runs the relayer
func (s *CosmosRelayerTestSuite) Test_RelayerInfo() {
	ctx := context.Background()
	s.SetupSuite(ctx)

	s.Require().True(s.Run("Verify Chain A to Chain B Relayer Info", func() {
		info, err := s.RelayerClient.Info(context.Background(), &relayertypes.InfoRequest{
			SrcChain: s.SimdA.Config().ChainID,
			DstChain: s.SimdB.Config().ChainID,
		})
		s.Require().NoError(err)
		s.Require().NotNil(info)
		s.Require().Equal(s.SimdA.Config().ChainID, info.SourceChain.ChainId)
		s.Require().Equal(s.SimdB.Config().ChainID, info.TargetChain.ChainId)
	}))

	s.Require().True(s.Run("Verify Chain B to Chain A Relayer Info", func() {
		info, err := s.RelayerClient.Info(context.Background(), &relayertypes.InfoRequest{
			SrcChain: s.SimdB.Config().ChainID,
			DstChain: s.SimdA.Config().ChainID,
		})
		s.Require().NoError(err)
		s.Require().NotNil(info)
		s.Require().Equal(s.SimdB.Config().ChainID, info.SourceChain.ChainId)
		s.Require().Equal(s.SimdA.Config().ChainID, info.TargetChain.ChainId)
	}))
}

func (s *CosmosRelayerTestSuite) Test_ICS20RecvAndAckPacket() {
	ctx := context.Background()
	s.FilteredICS20RecvAndAckPacketTest(ctx, 1, nil)
}

func (s *CosmosRelayerTestSuite) Test_10_ICS20RecvAndAckPacket() {
	ctx := context.Background()
	s.FilteredICS20RecvAndAckPacketTest(ctx, 10, []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
}

func (s *CosmosRelayerTestSuite) Test_10_FilteredICS20RecvAndAckPacket() {
	ctx := context.Background()
	s.FilteredICS20RecvAndAckPacketTest(ctx, 10, []uint64{1, 5, 6, 7})
}

func (s *CosmosRelayerTestSuite) FilteredICS20RecvAndAckPacketTest(ctx context.Context, numOfTransfers int, recvAndAckFilter []uint64) {
	s.Require().GreaterOrEqual(numOfTransfers, len(recvAndAckFilter))
	s.Require().Greater(numOfTransfers, 0)

	s.SetupSuite(ctx)

	simdAUser, simdBUser := s.CosmosUsers[0], s.CosmosUsers[1]
	transferAmount := big.NewInt(testvalues.TransferAmount)
	totalTransferAmount := testvalues.TransferAmount * int64(numOfTransfers)
	var relayedAmount int64
	if len(recvAndAckFilter) == 0 {
		relayedAmount = totalTransferAmount
	} else {
		relayedAmount = testvalues.TransferAmount * int64(len(recvAndAckFilter))
	}

	var txHashes [][]byte
	s.Require().True(s.Run("Send transfers on Chain A", func() {
		for range numOfTransfers {
			timeout := uint64(time.Now().Add(30 * time.Minute).Unix())
			transferCoin := sdk.NewCoin(s.SimdA.Config().Denom, sdkmath.NewIntFromBigInt(transferAmount))

			transferPayload := transfertypes.FungibleTokenPacketData{
				Denom:    transferCoin.Denom,
				Amount:   transferCoin.Amount.String(),
				Sender:   simdAUser.FormattedAddress(),
				Receiver: simdBUser.FormattedAddress(),
				Memo:     "",
			}

			payload := channeltypesv2.Payload{
				SourcePort:      transfertypes.PortID,
				DestinationPort: transfertypes.PortID,
				Version:         transfertypes.V1,
				Encoding:        transfertypes.EncodingJSON,
				Value:           transferPayload.GetBytes(),
			}
			msgSendPacket := channeltypesv2.MsgSendPacket{
				SourceClient:     ibctesting.FirstClientID,
				TimeoutTimestamp: timeout,
				Payloads: []channeltypesv2.Payload{
					payload,
				},
				Signer: simdAUser.FormattedAddress(),
			}

			resp, err := s.BroadcastMessages(ctx, s.SimdA, simdAUser, 200_000, &msgSendPacket)
			s.Require().NoError(err)
			s.Require().NotEmpty(resp.TxHash)

			txHash, err := hex.DecodeString(resp.TxHash)
			s.Require().NoError(err)
			s.Require().NotEmpty(txHash)

			txHashes = append(txHashes, txHash)
		}

		s.Require().True(s.Run("Verify balances on Chain A", func() {
			resp, err := e2esuite.GRPCQuery[banktypes.QueryBalanceResponse](ctx, s.SimdA, &banktypes.QueryBalanceRequest{
				Address: simdAUser.FormattedAddress(),
				Denom:   s.SimdA.Config().Denom,
			})
			s.Require().NoError(err)
			s.Require().NotNil(resp.Balance)
			s.Require().Equal(testvalues.InitialBalance-totalTransferAmount, resp.Balance.Amount.Int64())
		}))
	}))

	var ackTxHash []byte
	s.Require().True(s.Run("Receive packets on Chain B", func() {
		var txBodyBz []byte
		s.Require().True(s.Run("Retrieve relay tx", func() {
			resp, err := s.RelayerClient.RelayByTx(context.Background(), &relayertypes.RelayByTxRequest{
				SrcChain:           s.SimdA.Config().ChainID,
				DstChain:           s.SimdB.Config().ChainID,
				SourceTxIds:        txHashes,
				SrcClientId:        ibctesting.FirstClientID,
				DstClientId:        ibctesting.FirstClientID,
				SrcPacketSequences: recvAndAckFilter,
			})
			s.Require().NoError(err)
			s.Require().NotEmpty(resp.Tx)
			s.Require().Empty(resp.Address)

			txBodyBz = resp.Tx
		}))

		s.Require().True(s.Run("Broadcast relay tx", func() {
			resp := s.MustBroadcastSdkTxBody(ctx, s.SimdB, s.SimdBSubmitter, 2_000_000, txBodyBz)

			var err error
			ackTxHash, err = hex.DecodeString(resp.TxHash)
			s.Require().NoError(err)
			s.Require().NotEmpty(ackTxHash)

			s.Require().True(s.Run("Verify balances on Chain B", func() {
				ibcDenom := transfertypes.NewDenom(s.SimdA.Config().Denom, transfertypes.NewHop(transfertypes.PortID, ibctesting.FirstClientID)).IBCDenom()
				// User balance on Cosmos chain
				resp, err := e2esuite.GRPCQuery[banktypes.QueryBalanceResponse](ctx, s.SimdB, &banktypes.QueryBalanceRequest{
					Address: simdBUser.FormattedAddress(),
					Denom:   ibcDenom,
				})
				s.Require().NoError(err)
				s.Require().NotNil(resp.Balance)
				s.Require().Equal(relayedAmount, resp.Balance.Amount.Int64())
				s.Require().Equal(ibcDenom, resp.Balance.Denom)
			}))
		}))
	}))

	s.Require().True(s.Run("Acknowledge packets on Chain A", func() {
		s.Require().True(s.Run("Verify commitments exists", func() {
			for _, seq := range recvAndAckFilter {
				resp, err := e2esuite.GRPCQuery[channeltypesv2.QueryPacketCommitmentResponse](ctx, s.SimdA, &channeltypesv2.QueryPacketCommitmentRequest{
					ClientId: ibctesting.FirstClientID,
					Sequence: seq,
				})
				s.Require().NoError(err)
				s.Require().NotEmpty(resp.Commitment)
			}
		}))

		var ackTxBodyBz []byte
		s.Require().True(s.Run("Retrieve ack tx to Chain A", func() {
			resp, err := s.RelayerClient.RelayByTx(context.Background(), &relayertypes.RelayByTxRequest{
				SrcChain:           s.SimdB.Config().ChainID,
				DstChain:           s.SimdA.Config().ChainID,
				SourceTxIds:        [][]byte{ackTxHash},
				SrcClientId:        ibctesting.FirstClientID,
				DstClientId:        ibctesting.FirstClientID,
				DstPacketSequences: recvAndAckFilter,
			})
			s.Require().NoError(err)
			s.Require().NotEmpty(resp.Tx)
			s.Require().Empty(resp.Address)

			ackTxBodyBz = resp.Tx
		}))

		s.Require().True(s.Run("Broadcast ack tx on Chain A", func() {
			_ = s.MustBroadcastSdkTxBody(ctx, s.SimdA, s.SimdASubmitter, 2_000_000, ackTxBodyBz)
		}))

		s.Require().True(s.Run("Verify commitments removed", func() {
			for _, seq := range recvAndAckFilter {
				_, err := e2esuite.GRPCQuery[channeltypesv2.QueryPacketCommitmentResponse](ctx, s.SimdA, &channeltypesv2.QueryPacketCommitmentRequest{
					ClientId: ibctesting.FirstClientID,
					Sequence: seq,
				})
				s.Require().ErrorContains(err, "packet commitment hash not found")
			}
		}))
	}))
}

func (s *CosmosRelayerTestSuite) Test_ICS20TimeoutPacket() {
	ctx := context.Background()
	s.FilteredICS20TimeoutPacketTest(ctx, 1, nil)
}

func (s *CosmosRelayerTestSuite) Test_10_ICS20TimeoutPacket() {
	ctx := context.Background()
	s.FilteredICS20TimeoutPacketTest(ctx, 10, nil)
}

func (s *CosmosRelayerTestSuite) Test_10_FilteredICS20TimeoutPacket() {
	ctx := context.Background()
	s.FilteredICS20TimeoutPacketTest(ctx, 10, []uint64{1})
}

func (s *CosmosRelayerTestSuite) FilteredICS20TimeoutPacketTest(ctx context.Context, numOfTransfers int, timeoutFilter []uint64) {
	s.Require().Greater(numOfTransfers, len(timeoutFilter))

	s.SetupSuite(ctx)

	simdAUser, simdBUser := s.CosmosUsers[0], s.CosmosUsers[1]
	transferAmount := big.NewInt(testvalues.TransferAmount)
	totalTransferAmount := testvalues.TransferAmount * int64(numOfTransfers)
	var refundedAmount int64
	if len(timeoutFilter) == 0 {
		refundedAmount = totalTransferAmount
	} else {
		refundedAmount = testvalues.TransferAmount * int64(len(timeoutFilter))
	}

	var txHashes [][]byte
	s.Require().True(s.Run("Send transfers on Chain A", func() {
		for range numOfTransfers {
			timeout := uint64(time.Now().Add(30 * time.Second).Unix())
			transferCoin := sdk.NewCoin(s.SimdA.Config().Denom, sdkmath.NewIntFromBigInt(transferAmount))

			transferPayload := transfertypes.FungibleTokenPacketData{
				Denom:    transferCoin.Denom,
				Amount:   transferCoin.Amount.String(),
				Sender:   simdAUser.FormattedAddress(),
				Receiver: simdBUser.FormattedAddress(),
				Memo:     "",
			}

			payload := channeltypesv2.Payload{
				SourcePort:      transfertypes.PortID,
				DestinationPort: transfertypes.PortID,
				Version:         transfertypes.V1,
				Encoding:        transfertypes.EncodingJSON,
				Value:           transferPayload.GetBytes(),
			}
			msgSendPacket := channeltypesv2.MsgSendPacket{
				SourceClient:     ibctesting.FirstClientID,
				TimeoutTimestamp: timeout,
				Payloads: []channeltypesv2.Payload{
					payload,
				},
				Signer: simdAUser.FormattedAddress(),
			}

			resp, err := s.BroadcastMessages(ctx, s.SimdA, simdAUser, 200_000, &msgSendPacket)
			s.Require().NoError(err)
			s.Require().NotEmpty(resp.TxHash)

			txHash, err := hex.DecodeString(resp.TxHash)
			s.Require().NoError(err)
			s.Require().NotEmpty(txHash)

			txHashes = append(txHashes, txHash)
		}

		s.Require().True(s.Run("Verify balances on Chain A", func() {
			resp, err := e2esuite.GRPCQuery[banktypes.QueryBalanceResponse](ctx, s.SimdA, &banktypes.QueryBalanceRequest{
				Address: simdAUser.FormattedAddress(),
				Denom:   s.SimdA.Config().Denom,
			})
			s.Require().NoError(err)
			s.Require().NotNil(resp.Balance)
			s.Require().Equal(testvalues.InitialBalance-totalTransferAmount, resp.Balance.Amount.Int64())
		}))
	}))

	// Prefetching the relay tx before the timeout
	var txBodyBz []byte
	s.Require().True(s.Run("Retrieve relay tx", func() {
		resp, err := s.RelayerClient.RelayByTx(context.Background(), &relayertypes.RelayByTxRequest{
			SrcChain:    s.SimdA.Config().ChainID,
			DstChain:    s.SimdB.Config().ChainID,
			SourceTxIds: txHashes,
			SrcClientId: ibctesting.FirstClientID,
			DstClientId: ibctesting.FirstClientID,
		})
		s.Require().NoError(err)
		s.Require().NotEmpty(resp.Tx)
		s.Require().Empty(resp.Address)

		txBodyBz = resp.Tx
	}))

	// Wait until timeout
	time.Sleep(30 * time.Second)

	s.Require().True(s.Run("Timeout packet on Chain A", func() {
		var timeoutTxBodyBz []byte
		s.Require().True(s.Run("Retrieve timeout tx", func() {
			resp, err := s.RelayerClient.RelayByTx(context.Background(), &relayertypes.RelayByTxRequest{
				SrcChain:           s.SimdB.Config().ChainID,
				DstChain:           s.SimdA.Config().ChainID,
				TimeoutTxIds:       txHashes,
				SrcClientId:        ibctesting.FirstClientID,
				DstClientId:        ibctesting.FirstClientID,
				DstPacketSequences: timeoutFilter,
			})
			s.Require().NoError(err)
			s.Require().NotEmpty(resp.Tx)
			s.Require().Empty(resp.Address)

			timeoutTxBodyBz = resp.Tx
		}))

		s.Require().True(s.Run("Broadcast timeout tx", func() {
			_ = s.MustBroadcastSdkTxBody(ctx, s.SimdA, s.SimdASubmitter, 2_000_000, timeoutTxBodyBz)
		}))

		s.Require().True(s.Run("Verify balances on Chain A", func() {
			resp, err := e2esuite.GRPCQuery[banktypes.QueryBalanceResponse](ctx, s.SimdA, &banktypes.QueryBalanceRequest{
				Address: simdAUser.FormattedAddress(),
				Denom:   s.SimdA.Config().Denom,
			})
			s.Require().NoError(err)
			s.Require().NotNil(resp.Balance)
			s.Require().Equal(testvalues.InitialBalance-totalTransferAmount+refundedAmount, resp.Balance.Amount.Int64())
		}))
	}))

	s.Require().True(s.Run("Constructing relay packet after timeout should fail", func() {
		resp, err := s.RelayerClient.RelayByTx(context.Background(), &relayertypes.RelayByTxRequest{
			SrcChain:     s.SimdA.Config().ChainID,
			DstChain:     s.SimdB.Config().ChainID,
			TimeoutTxIds: txHashes,
			SrcClientId:  ibctesting.FirstClientID,
			DstClientId:  ibctesting.FirstClientID,
		})
		s.Require().Error(err)
		s.Require().Nil(resp)
	}))

	s.Require().True(s.Run("Receiving packets on Chain B after timeout should fail", func() {
		resp, err := s.BroadcastSdkTxBody(ctx, s.SimdB, s.SimdBSubmitter, 2_000_000, txBodyBz)
		s.Require().ErrorContains(err, "timeout elapsed")
		s.Require().Nil(resp)
	}))
}

func (s *CosmosRelayerTestSuite) Test_UpdateClient() {
	ctx := context.Background()
	s.SetupSuite(ctx)

	var initialHeight uint64
	s.Require().True(s.Run("Get the initial height", func() {
		resp, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, s.SimdA, &clienttypes.QueryClientStateRequest{
			ClientId: ibctesting.FirstClientID,
		})
		s.Require().NoError(err)
		s.Require().NotNil(resp.ClientState)

		var tmClientState ibctmtypes.ClientState
		err = proto.Unmarshal(resp.ClientState.Value, &tmClientState)
		s.Require().NoError(err)
		s.Require().NotZero(tmClientState.LatestHeight.RevisionHeight)

		initialHeight = tmClientState.LatestHeight.RevisionHeight
	}))

	s.Require().True(s.Run("Update client on Chain A", func() {
		var updateTxBodyBz []byte
		s.Require().True(s.Run("Retrieve update client tx", func() {
			resp, err := s.RelayerClient.UpdateClient(context.Background(), &relayertypes.UpdateClientRequest{
				SrcChain:    s.SimdB.Config().ChainID,
				DstChain:    s.SimdA.Config().ChainID,
				DstClientId: ibctesting.FirstClientID,
			})
			s.Require().NoError(err)
			s.Require().NotEmpty(resp.Tx)
			s.Require().Empty(resp.Address)

			updateTxBodyBz = resp.Tx
			
			// Generate Solana fixtures if enabled
			if s.generateSolanaFixtures {
				s.T().Log("🔧 Generating Solana fixtures from update client transaction")
				s.generateSolanaFixturesFromUpdateTx(ctx, updateTxBodyBz)
			}
		}))

		s.Require().True(s.Run("Broadcast update client tx", func() {
			_ = s.MustBroadcastSdkTxBody(ctx, s.SimdA, s.SimdASubmitter, 2_000_000, updateTxBodyBz)
		}))

		s.Require().True(s.Run("Verify client update on Chain A", func() {
			resp, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, s.SimdA, &clienttypes.QueryClientStateRequest{
				ClientId: ibctesting.FirstClientID,
			})
			s.Require().NoError(err)
			s.Require().NotNil(resp.ClientState)

			var tmClientState ibctmtypes.ClientState
			err = proto.Unmarshal(resp.ClientState.Value, &tmClientState)
			s.Require().NoError(err)
			s.Require().Greater(tmClientState.LatestHeight.RevisionHeight, initialHeight)
		}))
	}))
}

// generateSolanaFixturesFromUpdateTx extracts the update client data and generates Solana fixtures
func (s *CosmosRelayerTestSuite) generateSolanaFixturesFromUpdateTx(ctx context.Context, updateTxBodyBz []byte) {
	s.T().Log("🔍 Parsing update client transaction")
	
	// Parse the transaction body
	var txBody txtypes.TxBody
	err := proto.Unmarshal(updateTxBodyBz, &txBody)
	s.Require().NoError(err)
	s.Require().Len(txBody.Messages, 1, "Expected exactly one message in update client tx")
	
	// Extract the MsgUpdateClient
	var msgUpdateClient clienttypes.MsgUpdateClient
	err = proto.Unmarshal(txBody.Messages[0].Value, &msgUpdateClient)
	s.Require().NoError(err)
	
	s.T().Logf("📊 Found MsgUpdateClient for client: %s", msgUpdateClient.ClientId)
	
	// Extract the client message (this is the protobuf header that Solana needs)
	clientMessage := msgUpdateClient.ClientMessage
	s.Require().NotNil(clientMessage)
	
	// Generate fixtures
	s.generateSolanaClientStateFixture(ctx)
	s.generateSolanaConsensusStateFixture(ctx)
	s.generateSolanaUpdateClientMessageFixture(clientMessage)
	
	s.T().Log("✅ Solana fixtures generated successfully")
}

// generateSolanaClientStateFixture generates a client state fixture for Solana
func (s *CosmosRelayerTestSuite) generateSolanaClientStateFixture(ctx context.Context) {
	s.T().Log("🔧 Generating ClientState fixture")
	
	// Query the client state from SimdA
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryClientStateResponse](ctx, s.SimdA, &clienttypes.QueryClientStateRequest{
		ClientId: ibctesting.FirstClientID,
	})
	s.Require().NoError(err)
	s.Require().NotNil(resp.ClientState)
	
	// Unmarshal the Tendermint client state
	var tmClientState ibctmtypes.ClientState
	err = proto.Unmarshal(resp.ClientState.Value, &tmClientState)
	s.Require().NoError(err)
	
	// Convert to Solana format
	solanaClientState := map[string]interface{}{
		"chain_id":                  tmClientState.ChainId,
		"trust_level_numerator":     tmClientState.TrustLevel.Numerator,
		"trust_level_denominator":   tmClientState.TrustLevel.Denominator,
		"trusting_period":           tmClientState.TrustingPeriod.Seconds(),
		"unbonding_period":          tmClientState.UnbondingPeriod.Seconds(),
		"max_clock_drift":           tmClientState.MaxClockDrift.Seconds(),
		"frozen_height":             tmClientState.FrozenHeight.RevisionHeight,
		"latest_height":             tmClientState.LatestHeight.RevisionHeight,
		"metadata": map[string]interface{}{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"source":       "real_cosmos_chain",
			"description":  fmt.Sprintf("Client state for %s captured from %s", tmClientState.ChainId, s.SimdA.Config().ChainID),
		},
	}
	
	// Save to file
	filename := filepath.Join(s.solanaFixtureDir, "client_state.json")
	s.saveJsonFixture(filename, solanaClientState)
	s.T().Logf("💾 Client state fixture saved: %s", filename)
}

// generateSolanaConsensusStateFixture generates a consensus state fixture for Solana
func (s *CosmosRelayerTestSuite) generateSolanaConsensusStateFixture(ctx context.Context) {
	s.T().Log("🔧 Generating ConsensusState fixture")
	
	// Query the consensus state from SimdA
	resp, err := e2esuite.GRPCQuery[clienttypes.QueryConsensusStateResponse](ctx, s.SimdA, &clienttypes.QueryConsensusStateRequest{
		ClientId:     ibctesting.FirstClientID,
		RevisionNumber: 1,
		RevisionHeight: 1,
		LatestHeight:   true,
	})
	s.Require().NoError(err)
	s.Require().NotNil(resp.ConsensusState)
	
	// Unmarshal the Tendermint consensus state
	var tmConsensusState ibctmtypes.ConsensusState
	err = proto.Unmarshal(resp.ConsensusState.Value, &tmConsensusState)
	s.Require().NoError(err)
	
	// Convert to Solana format
	solanaConsensusState := map[string]interface{}{
		"timestamp":             tmConsensusState.Timestamp.UnixNano(),
		"root":                  hex.EncodeToString(tmConsensusState.Root.GetHash()),
		"next_validators_hash":  hex.EncodeToString(tmConsensusState.NextValidatorsHash),
		"metadata": map[string]interface{}{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"source":       "real_cosmos_chain",
			"description":  fmt.Sprintf("Consensus state captured from %s", s.SimdA.Config().ChainID),
		},
	}
	
	// Save to file
	filename := filepath.Join(s.solanaFixtureDir, "consensus_state.json")
	s.saveJsonFixture(filename, solanaConsensusState)
	s.T().Logf("💾 Consensus state fixture saved: %s", filename)
}

// generateSolanaUpdateClientMessageFixture generates the update client message fixture for Solana
func (s *CosmosRelayerTestSuite) generateSolanaUpdateClientMessageFixture(clientMessage *types.Any) {
	s.T().Log("🔧 Generating UpdateClientMessage fixture")
	
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
	filename := filepath.Join(s.solanaFixtureDir, "update_client_message.json")
	s.saveJsonFixture(filename, updateClientMessage)
	s.T().Logf("💾 Update client message fixture saved: %s", filename)
}

// saveJsonFixture saves a fixture as JSON
func (s *CosmosRelayerTestSuite) saveJsonFixture(filename string, data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	s.Require().NoError(err)
	
	err = os.WriteFile(filename, jsonData, 0644)
	s.Require().NoError(err)
}
