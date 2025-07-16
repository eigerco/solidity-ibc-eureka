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

    let update_msg = UpdateClientMsg {
        client_message: load_update_client_message_from_fixture(),
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

    let update_result = env
        .program
        .request()
        .accounts(ics07_tendermint::accounts::UpdateClient {
            client_state: contract.client_data_pda,
            trusted_consensus_state,
            new_consensus_state_store,
            payer: env.payer.pubkey(),
            system_program: solana_system_interface::program::ID,
        })
        .args(ics07_tendermint::instruction::UpdateClient { msg: update_msg })
        .send();

    match update_result {
        Ok(sig) => log(&env, &format!("✅ Update client successful: {}", sig)),
        Err(e) => panic!("❌ Failed to update client: {}", e),
    }
}
