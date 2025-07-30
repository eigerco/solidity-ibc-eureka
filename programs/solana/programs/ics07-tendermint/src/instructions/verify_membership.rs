use crate::error::ErrorCode;
use crate::helpers::{deserialize_merkle_proof, validate_proof_params};
use crate::VerifyMembership;
use anchor_lang::prelude::*;
use solana_light_client_interface::MembershipMsg;
use tendermint_light_client_membership::KVPair;

pub fn verify_membership(ctx: Context<VerifyMembership>, msg: MembershipMsg) -> Result<()> {
    require!(!msg.value.is_empty(), ErrorCode::MembershipEmptyValue);

    let client_state = &ctx.accounts.client_state;
    let consensus_state_store = &ctx.accounts.consensus_state_at_height;

    validate_proof_params(client_state, consensus_state_store, &msg)?;

    let proof = deserialize_merkle_proof(&msg.proof)?;
    let kv_pair = KVPair::new(msg.path, msg.value);
    let app_hash = consensus_state_store.consensus_state.root;

    tendermint_light_client_membership::membership(app_hash, &[(kv_pair, proof)]).map_err(|e| {
        msg!("Membership verification failed: {:?}", e);
        error!(ErrorCode::MembershipVerificationFailed)
    })?;

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
                    data: final_client_state_data,
                    owner: crate::ID,
                    executable: false,
                    rent_epoch: 0,
                },
            ),
            (
                consensus_state_store_pda,
                Account {
                    lamports: 1_000_000_000,
                    data: final_consensus_state_data,
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

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");

        let checks = vec![
            Check::success(),
            Check::account(&test_accounts.client_state_pda)
                .owner(&crate::ID)
                .build(),
            Check::account(&test_accounts.consensus_state_store_pda)
                .owner(&crate::ID)
                .build(),
        ];

        let result = mollusk.process_and_validate_instruction(&instruction, &test_accounts.accounts, &checks);
        
        println!("✅ Membership verification successful for predefined key");
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
