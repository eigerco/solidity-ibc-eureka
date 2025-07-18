use anchor_client::solana_sdk::pubkey::Pubkey;
use anchor_client::solana_sdk::signer::Signer;
use ics07_tendermint::UpdateClientMsg;
use std::str::FromStr;

use crate::helpers::{
    initialize_contract, load_client_state_from_fixture, load_consensus_state_from_fixture,
    load_update_client_message_from_fixture, log, setup_test_env,
};

#[test]
fn test_update_client() {
    let program_id = Pubkey::from_str("8wQAC7oWLTxExhR49jYAzXZB39mu7WVVvkWJGgAMMjpV").unwrap();

    // Load real fixtures from e2e test data
    let client_state = load_client_state_from_fixture();
    let consensus_state = load_consensus_state_from_fixture();

    let env = setup_test_env(program_id);
    let contract = initialize_contract(&env, program_id, client_state, consensus_state);

    let client_message_bytes = load_update_client_message_from_fixture();
    log(&env, &format!("📏 Client message size: {} bytes", client_message_bytes.len()));
    
    // Test deserialization to verify the header is correctly formatted
    log(&env, "🔍 Testing header deserialization...");
    match ics07_tendermint::helpers::deserialize_header(&client_message_bytes) {
        Ok(header) => {
            log(&env, "✅ Header deserialization successful!");
            log(&env, &format!("📊 Header trusted height: {}", header.trusted_height.revision_height()));
            log(&env, &format!("📊 Header signed height: {}", header.signed_header.header.height.value()));
            log(&env, &format!("📊 Header chain id: {}", header.signed_header.header.chain_id));
            log(&env, &format!("📊 Header time: {:?}", header.signed_header.header.time));
            log(&env, &format!("📊 Header last block id hash: {:?}", header.signed_header.header.last_block_id.as_ref().map(|bid| &bid.hash)));
            log(&env, &format!("📊 Header last commit hash: {:?}", header.signed_header.header.last_commit_hash));
            log(&env, &format!("📊 Header data hash: {:?}", header.signed_header.header.data_hash));
            log(&env, &format!("📊 Header validators hash: {:?}", header.signed_header.header.validators_hash));
            log(&env, &format!("📊 Header next validators hash: {:?}", header.signed_header.header.next_validators_hash));
            log(&env, &format!("📊 Header consensus hash: {:?}", header.signed_header.header.consensus_hash));
            log(&env, &format!("📊 Header app hash: {:?}", header.signed_header.header.app_hash));
            log(&env, &format!("📊 Header last results hash: {:?}", header.signed_header.header.last_results_hash));
            log(&env, &format!("📊 Header evidence hash: {:?}", header.signed_header.header.evidence_hash));
            log(&env, &format!("📊 Header proposer address: {:?}", header.signed_header.header.proposer_address));
            log(&env, &format!("📊 Header version: block={}, app={}", header.signed_header.header.version.block, header.signed_header.header.version.app));
            log(&env, &format!("📊 Validator set size: {}", header.validator_set.validators().len()));
            log(&env, &format!("📊 Trusted next validator set size: {}", header.trusted_next_validator_set.validators().len()));
            log(&env, &format!("📊 Commit signatures count: {}", header.signed_header.commit.signatures.len()));
        }
        Err(e) => {
            log(&env, &format!("❌ Header deserialization failed: {:?}", e));
            panic!("Header deserialization failed, cannot proceed with test");
        }
    }

    let update_msg = UpdateClientMsg {
        client_message: client_message_bytes.clone(),
    };

    // Get the client's current state to calculate the consensus state PDA
    let client_account = env
        .program
        .account::<ics07_tendermint::ClientState>(contract.client_data_pda)
        .expect("Failed to fetch client_data account");
    let new_height = client_account.latest_height.revision_height + 1; // Assuming next height

    // Calculate the consensus state store PDA for the new height
    let (new_consensus_state_store, _bump) = Pubkey::find_program_address(
        &[
            b"consensus_state",
            contract.client_data_pda.as_ref(),
            &new_height.to_le_bytes(),
        ],
        &env.program.id(),
    );

    // Calculate the trusted consensus state PDA (using the current height)
    let (trusted_consensus_state, _bump) = Pubkey::find_program_address(
        &[
            b"consensus_state",
            contract.client_data_pda.as_ref(),
            &client_account.latest_height.revision_height.to_le_bytes(),
        ],
        &env.program.id(),
    );

    // Create the accounts and instruction structs to measure their sizes
    let accounts_struct = ics07_tendermint::accounts::UpdateClient {
        client_state: contract.client_data_pda,
        trusted_consensus_state,
        new_consensus_state_store,
        payer: env.payer.pubkey(),
        system_program: solana_system_interface::program::ID,
    };

    let instruction_struct = ics07_tendermint::instruction::UpdateClient {
        msg: update_msg.clone(),
    };

    // Log sizes of entire transaction components
    log(
        &env,
        &format!(
            "📏 Complete accounts struct size: {} bytes",
            std::mem::size_of_val(&accounts_struct)
        ),
    );
    log(
        &env,
        &format!(
            "📏 Complete instruction struct size: {} bytes",
            std::mem::size_of_val(&instruction_struct)
        ),
    );
    log(
        &env,
        &format!(
            "📏 UpdateClient msg within instruction: {} bytes",
            std::mem::size_of_val(&update_msg)
        ),
    );

    log(
        &env,
        &format!(
            "📏 client_message field size: {} bytes",
            client_message_bytes.len()
        ),
    );

    let update_result = env
        .program
        .request()
        .accounts(accounts_struct)
        .args(instruction_struct)
        .send();

    match update_result {
        Ok(sig) => log(&env, &format!("✅ Update client successful: {}", sig)),
        Err(e) => panic!("❌ Failed to update client: {}", e),
    }
}
