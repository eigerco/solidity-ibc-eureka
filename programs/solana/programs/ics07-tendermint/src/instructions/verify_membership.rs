use crate::error::ErrorCode;
use crate::helpers::{deserialize_merkle_proof, validate_proof_params};
use crate::types::MembershipMsg;
use crate::VerifyMembership;
use anchor_lang::prelude::*;
use tendermint_light_client_membership::KVPair;

pub fn verify_membership(ctx: Context<VerifyMembership>, msg: MembershipMsg) -> Result<()> {
    msg!("🚀 VERIFY_MEMBERSHIP FUNCTION CALLED!");
    
    require!(!msg.value.is_empty(), ErrorCode::MembershipEmptyValue);

    let client_state = &ctx.accounts.client_state;
    let consensus_state_store = &ctx.accounts.consensus_state_at_height;

    msg!("📝 About to validate proof params");
    validate_proof_params(client_state, consensus_state_store, &msg)?;

    msg!("🔍 About to deserialize merkle proof, proof size: {}", msg.proof.len());
    let proof = deserialize_merkle_proof(&msg.proof)?;
    msg!("✅ Proof deserialized successfully, proof.proofs.len(): {}", proof.proofs.len());
    
    let kv_pair = KVPair::new(msg.path.clone(), msg.value.clone());
    msg!("📦 KVPair created, path segments: {}, value len: {}", kv_pair.path.len(), kv_pair.value.len());
    
    // Debug: Print exact path bytes
    for (i, segment) in kv_pair.path.iter().enumerate() {
        msg!("Path segment {}: {:?}", i, segment);
        if let Ok(s) = std::str::from_utf8(segment) {
            msg!("Path segment {} as string: '{}'", i, s);
        }
    }
    
    let app_hash = consensus_state_store.consensus_state.root;
    msg!("🔑 App hash: {:?}", app_hash);

    msg!("⚡ About to call tendermint_light_client_membership::membership");
    tendermint_light_client_membership::membership(app_hash, &[(kv_pair, proof)]).map_err(|e| {
        msg!("❌ Membership verification failed: {:?}", e);
        error!(ErrorCode::MembershipVerificationFailed)
    })?;

    msg!("✅ Membership verification completed successfully!");
    Ok(())
}
