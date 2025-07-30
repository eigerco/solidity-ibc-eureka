use crate::error::ErrorCode;
use crate::state::ConsensusStateStore;
use crate::types::ClientState;
use anchor_lang::prelude::*;
use ibc_client_tendermint::types::{Header, Misbehaviour};
use ibc_core_commitment_types::merkle::MerkleProof;
use ibc_proto::ibc::core::commitment::v1::MerkleProof as RawMerkleProof;
use ibc_proto::ibc::lightclients::tendermint::v1::Misbehaviour as RawMisbehaviour;
use ibc_proto::{ibc::lightclients::tendermint::v1::Header as RawHeader, Protobuf};
use solana_light_client_interface::MembershipMsg;

pub fn deserialize_header(bytes: &[u8]) -> Result<Header> {
    <Header as Protobuf<RawHeader>>::decode_vec(bytes).map_err(|_| error!(ErrorCode::InvalidHeader))
}

pub fn deserialize_merkle_proof(bytes: &[u8]) -> Result<MerkleProof> {
    msg!("deserialize_merkle_proof: Starting deserialization of {} bytes", bytes.len());
    
    let result = <MerkleProof as Protobuf<RawMerkleProof>>::decode_vec(bytes)
        .map_err(|e| {
            msg!("deserialize_merkle_proof: Failed to decode: {:?}", e);
            error!(ErrorCode::InvalidProof)
        });
    
    match &result {
        Ok(_) => msg!("deserialize_merkle_proof: Deserialization successful"),
        Err(_) => msg!("deserialize_merkle_proof: Deserialization failed"),
    }
    
    result
}

pub fn deserialize_misbehaviour(bytes: &[u8]) -> Result<Misbehaviour> {
    <Misbehaviour as Protobuf<RawMisbehaviour>>::decode_vec(bytes)
        .map_err(|_| error!(ErrorCode::InvalidHeader))
}

pub fn validate_proof_params(
    client_state: &Account<ClientState>,
    consensus_state_store: &ConsensusStateStore,
    msg: &MembershipMsg,
) -> Result<()> {
    msg!("validate_proof_params: Starting validation");
    
    msg!("validate_proof_params: Checking if client is frozen");
    require!(!client_state.is_frozen(), ErrorCode::ClientFrozen);
    msg!("validate_proof_params: Client not frozen - OK");

    msg!("validate_proof_params: Checking height match - consensus: {}, msg: {}", 
         consensus_state_store.height, msg.height);
    require!(
        consensus_state_store.height == msg.height,
        ErrorCode::ProofHeightNotFound
    );
    msg!("validate_proof_params: Height match - OK");

    msg!("validate_proof_params: Checking height bounds - msg: {}, client latest: {}", 
         msg.height, client_state.latest_height.revision_height);
    require!(
        msg.height <= client_state.latest_height.revision_height,
        ErrorCode::InvalidHeight
    );
    msg!("validate_proof_params: Height bounds - OK");

    msg!("validate_proof_params: Checking delays - time: {}, block: {}", 
         msg.delay_time_period, msg.delay_block_period);
    require!(
        msg.delay_time_period == 0 && msg.delay_block_period == 0,
        ErrorCode::NonZeroDelay
    );
    msg!("validate_proof_params: Delays check - OK");

    msg!("validate_proof_params: All validations passed");
    Ok(())
}
