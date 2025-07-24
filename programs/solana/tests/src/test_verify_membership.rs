use anchor_client::solana_sdk::pubkey::Pubkey;
use anchor_client::solana_sdk::compute_budget::ComputeBudgetInstruction;
use std::str::FromStr;

use crate::helpers::{
    initialize_contract, load_membership_fixture_from_file, load_non_membership_fixture_from_file,
    log, setup_test_env,
};

#[test]
fn test_verify_membership() {
    let program_id = Pubkey::from_str("8wQAC7oWLTxExhR49jYAzXZB39mu7WVVvkWJGgAMMjpV").unwrap();

    // Load real fixtures from e2e test data (test first membership fixture)
    let (client_state, consensus_state, membership_msg) = load_membership_fixture_from_file(0);

    // Debug log the loaded data
    println!("🔍 Debug: Loaded membership data:");
    println!("   - Height: {}", membership_msg.height);
    println!("   - Path: {:?}", membership_msg.path);
    println!("   - Value length: {}", membership_msg.value.len());
    println!("   - Value bytes: {:?}", membership_msg.value);
    println!("   - Proof length: {}", membership_msg.proof.len());
    println!("   - Client state latest_height: {}", client_state.latest_height.revision_height);
    println!("   - Consensus state root: {:?}", consensus_state.root);

    let env = setup_test_env(program_id);

    // Clone client_state before passing to initialize_contract since we need to use it later
    let contract = initialize_contract(&env, program_id, client_state.clone(), consensus_state);

    let proof_height = membership_msg.height;

    // Get the consensus state store PDA using chain_id and proof height
    // This matches the updated program that uses chain_id instead of client_state.key()
    // For membership verification, we need the consensus state at the height where the proof was generated
    let (consensus_state_at_height, _bump) = Pubkey::find_program_address(
        &[
            b"consensus_state",
            contract.client_data_pda.as_ref(),
            &proof_height.to_le_bytes(),
        ],
        &env.program.id(),
    );

    // Request more compute units for expensive merkle proof verification
    let compute_budget_ix = ComputeBudgetInstruction::set_compute_unit_limit(1_400_000);
    
    let verify_result = env
        .program
        .request()
        .instruction(compute_budget_ix)
        .accounts(ics07_tendermint::accounts::VerifyMembership {
            client_state: contract.client_data_pda,
            consensus_state_at_height,
        })
        .args(ics07_tendermint::instruction::VerifyMembership {
            msg: membership_msg,
        })
        .send();

    match verify_result {
        Ok(sig) => log(&env, &format!("✅ Verify membership successful: {}", sig)),
        Err(e) => panic!("❌ Failed to verify membership: {}", e),
    }
}

// #[test]
// fn test_verify_non_membership() {
//     let program_id = Pubkey::from_str("8wQAC7oWLTxExhR49jYAzXZB39mu7WVVvkWJGgAMMjpV").unwrap();

//     // Load real fixtures from e2e test data (test first non-membership fixture)
//     let (client_state, consensus_state, membership_msg) = load_non_membership_fixture_from_file(0);

//     let env = setup_test_env(program_id);
//     let contract = initialize_contract(&env, program_id, client_state, consensus_state);

//     let proof_height = membership_msg.height;

//     // Get the consensus state store PDA for the proof height
//     let (consensus_state_at_height, _bump) = Pubkey::find_program_address(
//         &[
//             b"consensus_state",
//             contract.client_data_pda.as_ref(),
//             &proof_height.to_le_bytes(),
//         ],
//         &env.program.id(),
//     );

//     let verify_result = env
//         .program
//         .request()
//         .accounts(ics07_tendermint::accounts::VerifyNonMembership {
//             client_state: contract.client_data_pda,
//             consensus_state_at_height,
//         })
//         .args(ics07_tendermint::instruction::VerifyNonMembership {
//             msg: membership_msg,
//         })
//         .send();

//     match verify_result {
//         Ok(sig) => log(
//             &env,
//             &format!("✅ Verify non-membership successful: {}", sig),
//         ),
//         Err(e) => panic!("❌ Failed to verify non-membership: {}", e),
//     }
// }
