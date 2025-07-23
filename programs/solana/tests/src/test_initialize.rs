use anchor_client::solana_sdk::pubkey::Pubkey;
use std::str::FromStr;

use crate::helpers::{
    initialize_contract, load_client_state_from_fixture, load_consensus_state_from_fixture,
    setup_test_env,
};

#[test]
fn test_initialize_with_pda() {
    let program_id = Pubkey::from_str("8wQAC7oWLTxExhR49jYAzXZB39mu7WVVvkWJGgAMMjpV").unwrap();

    // Load real fixtures from e2e test data
    let client_state = load_client_state_from_fixture();
    let consensus_state = load_consensus_state_from_fixture();

    let env = setup_test_env(program_id);
    let contract = initialize_contract(&env, program_id, client_state.clone(), consensus_state);

    let account = env
        .program
        .account::<ics07_tendermint::ClientState>(contract.client_data_pda)
        .expect("Failed to fetch client_data account");

    assert_eq!(account.chain_id, contract.client_state.chain_id);
    assert_eq!(
        account.latest_height.revision_height,
        client_state.latest_height.revision_height
    );
    // Note: consensus state verification would require accessing the separate consensus state store
}
