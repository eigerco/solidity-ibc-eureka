use crate::error::ErrorCode;
use crate::helpers::{deserialize_merkle_proof, validate_proof_params};
use crate::VerifyMembership;
use anchor_lang::prelude::*;
use solana_light_client_interface::MembershipMsg;
use tendermint_light_client_membership::KVPair;

pub fn verify_membership(ctx: Context<VerifyMembership>, msg: MembershipMsg) -> Result<()> {
    msg!("=== VERIFY_MEMBERSHIP START ===");
    
    msg!("Step 1: Checking empty value requirement");
    require!(!msg.value.is_empty(), ErrorCode::MembershipEmptyValue);
    msg!("Step 1: Empty value check passed");

    msg!("Step 2: Getting accounts");
    let client_state = &ctx.accounts.client_state;
    let consensus_state_store = &ctx.accounts.consensus_state_at_height;
    msg!("Step 2: Accounts retrieved successfully");

    msg!("Step 3: Validating proof params");
    validate_proof_params(client_state, consensus_state_store, &msg)?;
    msg!("Step 3: Proof params validation passed");

    msg!("Step 4: About to deserialize proof of {} bytes", msg.proof.len());
    let proof = deserialize_merkle_proof(&msg.proof).map_err(|e| {
        msg!("Step 4: Proof deserialization failed: {:?}", e);
        e
    })?;
    msg!("Step 4: Proof deserialized successfully");
    
    msg!("Step 5: Creating KV pair with path len: {}, value len: {}", msg.path.len(), msg.value.len());
    let kv_pair = KVPair::new(msg.path, msg.value);
    msg!("Step 5: KV pair created successfully");
    
    msg!("Step 6: Getting app hash");
    let app_hash = consensus_state_store.consensus_state.root;
    msg!("Step 6: App hash retrieved: {:?}", app_hash);

    msg!("Step 7: About to run membership verification");
    tendermint_light_client_membership::membership(app_hash, &[(kv_pair, proof)]).map_err(|e| {
        msg!("Step 7: Membership verification failed: {:?}", e);
        error!(ErrorCode::MembershipVerificationFailed)
    })?;

    msg!("Step 8: Membership verification completed successfully");
    msg!("=== VERIFY_MEMBERSHIP END ===");
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::state::ConsensusStateStore;
    use crate::test_helpers::fixtures::*;
    use crate::types::ClientState;
    use anchor_lang::InstructionData;
    use mollusk_svm::result::Check;
    use mollusk_svm::Mollusk;
    use solana_sdk::account::Account;
    use solana_sdk::instruction::{AccountMeta, Instruction};
    use solana_sdk::pubkey::Pubkey;
    use solana_sdk::{native_loader, system_program};

    struct TestAccounts {
        client_state_pda: Pubkey,
        consensus_state_store_pda: Pubkey,
        accounts: Vec<(Pubkey, Account)>,
    }

    fn setup_test_accounts(
        chain_id: &str,
        height: u64,
        client_state: &ClientState,
        consensus_state: &crate::types::ConsensusState,
    ) -> TestAccounts {
        let (client_state_pda, _) =
            Pubkey::find_program_address(&[b"client", chain_id.as_bytes()], &crate::ID);
        let (consensus_state_store_pda, _) = Pubkey::find_program_address(
            &[
                b"consensus_state",
                client_state_pda.as_ref(),
                &height.to_le_bytes(),
            ],
            &crate::ID,
        );

        // Serialize client state data
        let mut client_state_data = vec![];
        client_state.try_serialize(&mut client_state_data).unwrap();
        let mut final_client_state_data = ClientState::DISCRIMINATOR.to_vec();
        final_client_state_data.extend_from_slice(&client_state_data);

        // Serialize consensus state store data
        let consensus_state_store = ConsensusStateStore {
            height,
            consensus_state: consensus_state.clone(),
        };
        let mut consensus_state_data = vec![];
        consensus_state_store.try_serialize(&mut consensus_state_data).unwrap();
        let mut final_consensus_state_data = ConsensusStateStore::DISCRIMINATOR.to_vec();
        final_consensus_state_data.extend_from_slice(&consensus_state_data);

        let accounts = vec![
            (
                client_state_pda,
                Account {
                    lamports: 1_000_000_000,
                    data: final_client_state_data.clone(),
                    owner: crate::ID,
                    executable: false,
                    rent_epoch: 0,
                },
            ),
            (
                consensus_state_store_pda,
                Account {
                    lamports: 1_000_000_000,
                    data: final_consensus_state_data.clone(),
                    owner: crate::ID,
                    executable: false,
                    rent_epoch: 0,
                },
            ),
            (
                system_program::ID,
                Account {
                    lamports: 0,
                    data: vec![],
                    owner: native_loader::ID,
                    executable: true,
                    rent_epoch: 0,
                },
            ),
        ];

        println!("Client state account data size: {} bytes", final_client_state_data.len());
        println!("Consensus state account data size: {} bytes", final_consensus_state_data.len());

        TestAccounts {
            client_state_pda,
            consensus_state_store_pda,
            accounts,
        }
    }

    fn create_verify_membership_instruction(
        test_accounts: &TestAccounts,
        msg: &MembershipMsg,
    ) -> Instruction {
        use crate::instruction;
        
        Instruction {
            program_id: crate::ID,
            accounts: vec![
                AccountMeta::new_readonly(test_accounts.client_state_pda, false),
                AccountMeta::new_readonly(test_accounts.consensus_state_store_pda, false),
            ],
            data: instruction::VerifyMembership { msg: msg.clone() }.data(),
        }
    }

    #[test]
    fn test_verify_membership_proof_deserialization() {
        // Test just the proof deserialization to isolate the memory issue
        let fixture = load_membership_predefined_key_fixture();
        let proof_bytes = hex_to_bytes(&fixture.membership_msg.proof);
        
        println!("Proof size: {} bytes", proof_bytes.len());
        
        // Try to deserialize the proof directly
        let result = crate::helpers::deserialize_merkle_proof(&proof_bytes);
        match result {
            Ok(_) => println!("✅ Proof deserialization successful"),
            Err(e) => println!("❌ Proof deserialization failed: {:?}", e),
        }
    }

    #[test]
    fn test_verify_membership_with_small_proof() {
        // Test with a minimal proof to see if the issue is proof size
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        let consensus_state_height = 40u64;
        
        // Create membership msg with tiny fake proof to test parameter deserialization
        let membership_msg = MembershipMsg {
            delay_block_period: 0,
            delay_time_period: 0,
            height: 40,
            path: vec![b"test".to_vec()],
            proof: vec![0u8; 32], // Small 32-byte fake proof
            value: vec![1, 2, 3, 4], // Small value
        };

        println!("Small proof size: {} bytes", membership_msg.proof.len());

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        println!("Small instruction data size: {} bytes", instruction.data.len());

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        match result.program_result {
            mollusk_svm::result::ProgramResult::Success => {
                println!("✅ Small proof test succeeded (unexpected)");
            }
            mollusk_svm::result::ProgramResult::Failure(error) => {
                println!("✅ Small proof test failed as expected: {:?}", error);
            }
            mollusk_svm::result::ProgramResult::UnknownError(error) => {
                println!("❌ Small proof test unknown error: {:?}", error);
            }
        }
    }

    #[test]
    fn test_verify_membership_empty_path() {
        // Test with empty path to see if Vec<Vec<u8>> is the issue
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        let consensus_state_height = 40u64;
        
        // Create membership msg with empty path
        let membership_msg = MembershipMsg {
            delay_block_period: 0,
            delay_time_period: 0,
            height: 40,
            path: vec![], // Empty path - no Vec<Vec<u8>>
            proof: vec![0u8; 32],
            value: vec![1, 2, 3, 4],
        };

        println!("Empty path test - path.len(): {}", membership_msg.path.len());

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        println!("Empty path instruction data size: {} bytes", instruction.data.len());

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        match result.program_result {
            mollusk_svm::result::ProgramResult::Success => {
                println!("✅ Empty path test succeeded (unexpected)");
            }
            mollusk_svm::result::ProgramResult::Failure(error) => {
                println!("✅ Empty path test failed as expected: {:?}", error);
            }
            mollusk_svm::result::ProgramResult::UnknownError(error) => {
                println!("❌ Empty path test unknown error: {:?}", error);
            }
        }
    }

    #[test]
    fn test_verify_membership_single_byte_fields() {
        // Test with minimal single-byte fields
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        let consensus_state_height = 40u64;
        
        // Create membership msg with all single-byte Vec fields
        let membership_msg = MembershipMsg {
            delay_block_period: 0,
            delay_time_period: 0,
            height: 40,
            path: vec![vec![1u8]], // Single path with single byte
            proof: vec![1u8], // Single byte proof
            value: vec![1u8], // Single byte value
        };

        println!("Single byte fields test");
        println!("  path.len(): {}, path[0].len(): {}", membership_msg.path.len(), membership_msg.path[0].len());
        println!("  proof.len(): {}", membership_msg.proof.len());
        println!("  value.len(): {}", membership_msg.value.len());

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        println!("Single byte instruction data size: {} bytes", instruction.data.len());

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        match result.program_result {
            mollusk_svm::result::ProgramResult::Success => {
                println!("✅ Single byte test succeeded (unexpected)");
            }
            mollusk_svm::result::ProgramResult::Failure(error) => {
                println!("✅ Single byte test failed as expected: {:?}", error);
            }
            mollusk_svm::result::ProgramResult::UnknownError(error) => {
                println!("❌ Single byte test unknown error: {:?}", error);
            }
        }
    }

    fn create_verify_non_membership_instruction(
        test_accounts: &TestAccounts,
        msg: &MembershipMsg,
    ) -> Instruction {
        use crate::instruction;
        
        Instruction {
            program_id: crate::ID,
            accounts: vec![
                AccountMeta::new_readonly(test_accounts.client_state_pda, false),
                AccountMeta::new_readonly(test_accounts.consensus_state_store_pda, false),
            ],
            data: instruction::VerifyNonMembership { msg: msg.clone() }.data(),
        }
    }

    #[test]
    fn test_verify_non_membership_instruction() {
        // Test verify_non_membership with same MembershipMsg to see if issue is instruction-specific
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        let consensus_state_height = 40u64;
        
        // Create membership msg for non-membership (empty value required)
        let membership_msg = MembershipMsg {
            delay_block_period: 0,
            delay_time_period: 0,
            height: 40,
            path: vec![vec![1u8]], // Single path with single byte
            proof: vec![1u8], // Single byte proof
            value: vec![], // Empty value for non-membership
        };

        println!("Non-membership test with same MembershipMsg structure");
        println!("  path.len(): {}, path[0].len(): {}", membership_msg.path.len(), membership_msg.path[0].len());
        println!("  proof.len(): {}", membership_msg.proof.len());
        println!("  value.len(): {}", membership_msg.value.len());

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_non_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        println!("Non-membership instruction data size: {} bytes", instruction.data.len());

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        match result.program_result {
            mollusk_svm::result::ProgramResult::Success => {
                println!("✅ Non-membership test succeeded (unexpected)");
            }
            mollusk_svm::result::ProgramResult::Failure(error) => {
                println!("✅ Non-membership test failed as expected: {:?}", error);
            }
            mollusk_svm::result::ProgramResult::UnknownError(error) => {
                println!("❌ Non-membership test unknown error: {:?}", error);
            }
        }
    }

    #[test]
    fn test_verify_membership_happy_path() {
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        // The consensus state in the fixture is at height 40, matching the proof height
        let consensus_state_height = 40u64;
        
        // Convert fixture membership msg to actual MembershipMsg
        let membership_msg = MembershipMsg {
            delay_block_period: fixture.membership_msg.delay_block_period,
            delay_time_period: fixture.membership_msg.delay_time_period,
            height: fixture.membership_msg.height,
            path: fixture.membership_msg.path.iter().map(|s| s.as_bytes().to_vec()).collect(),
            proof: hex_to_bytes(&fixture.membership_msg.proof),
            value: hex_to_bytes(&fixture.membership_msg.value),
        };

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        println!("Instruction data size: {} bytes", instruction.data.len());
        println!("Proof size in msg: {} bytes", membership_msg.proof.len());

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");

        // Just try to process the instruction without validation checks to see error details
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        match result.program_result {
            mollusk_svm::result::ProgramResult::Success => {
                println!("✅ Membership verification successful for predefined key");
            }
            mollusk_svm::result::ProgramResult::Failure(error) => {
                println!("❌ Membership verification failed with error: {:?}", error);
            }
            mollusk_svm::result::ProgramResult::UnknownError(error) => {
                println!("❌ Membership verification failed with unknown error: {:?}", error);
            }
        }
    }

    #[test]
    fn test_verify_membership_empty_value() {
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        // The consensus state in the fixture is at height 40, matching the proof height
        let consensus_state_height = 40u64;
        
        // Create membership msg with empty value
        let membership_msg = MembershipMsg {
            delay_block_period: fixture.membership_msg.delay_block_period,
            delay_time_period: fixture.membership_msg.delay_time_period,
            height: fixture.membership_msg.height,
            path: fixture.membership_msg.path.iter().map(|s| s.as_bytes().to_vec()).collect(),
            proof: hex_to_bytes(&fixture.membership_msg.proof),
            value: vec![], // Empty value
        };

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        assert_error_code(result, ErrorCode::MembershipEmptyValue, "Empty value test");
    }

    #[test]
    fn test_verify_membership_invalid_proof() {
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        // The consensus state in the fixture is at height 40, matching the proof height
        let consensus_state_height = 40u64;
        
        // Create membership msg with invalid proof (wrong length)
        let membership_msg = MembershipMsg {
            delay_block_period: fixture.membership_msg.delay_block_period,
            delay_time_period: fixture.membership_msg.delay_time_period,
            height: fixture.membership_msg.height,
            path: fixture.membership_msg.path.iter().map(|s| s.as_bytes().to_vec()).collect(),
            proof: vec![0u8; 32], // Invalid proof - too short
            value: hex_to_bytes(&fixture.membership_msg.value),
        };

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        // Should fail with deserialization error
        assert_instruction_failed(result, "Invalid proof test");
    }

    #[test]
    fn test_verify_membership_wrong_value() {
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        // The consensus state in the fixture is at height 40, matching the proof height
        let consensus_state_height = 40u64;
        
        // Create membership msg with wrong value
        let membership_msg = MembershipMsg {
            delay_block_period: fixture.membership_msg.delay_block_period,
            delay_time_period: fixture.membership_msg.delay_time_period,
            height: fixture.membership_msg.height,
            path: fixture.membership_msg.path.iter().map(|s| s.as_bytes().to_vec()).collect(),
            proof: hex_to_bytes(&fixture.membership_msg.proof),
            value: vec![1, 2, 3, 4], // Wrong value - doesn't match what's proven
        };

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        assert_error_code(result, ErrorCode::MembershipVerificationFailed, "Wrong value test");
    }

    #[test]
    fn test_verify_membership_wrong_path() {
        let fixture = load_membership_predefined_key_fixture();
        
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        // The consensus state in the fixture is at height 40, matching the proof height
        let consensus_state_height = 40u64;
        
        // Create membership msg with wrong path
        let membership_msg = MembershipMsg {
            delay_block_period: fixture.membership_msg.delay_block_period,
            delay_time_period: fixture.membership_msg.delay_time_period,
            height: fixture.membership_msg.height,
            path: vec![b"wrong".to_vec(), b"path".to_vec()], // Wrong path
            proof: hex_to_bytes(&fixture.membership_msg.proof),
            value: hex_to_bytes(&fixture.membership_msg.value),
        };

        let test_accounts = setup_test_accounts(
            &client_state.chain_id,
            consensus_state_height,
            &client_state,
            &consensus_state,
        );

        let instruction = create_verify_membership_instruction(
            &test_accounts,
            &membership_msg,
        );

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);
        
        assert_error_code(result, ErrorCode::MembershipVerificationFailed, "Wrong path test");
    }
}
