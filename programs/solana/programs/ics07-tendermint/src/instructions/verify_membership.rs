use crate::error::ErrorCode;
use crate::helpers::{deserialize_merkle_proof, validate_proof_params};
use crate::state::ConsensusStateStore;
use crate::types::ClientState;
use crate::VerifyMembership;
use anchor_lang::prelude::*;
use solana_light_client_interface::MembershipMsg;
use tendermint_light_client_membership::KVPair;

/// Size of Anchor's account discriminator in bytes
const ANCHOR_DISCRIMINATOR_SIZE: usize = 8;

pub fn verify_membership(ctx: Context<VerifyMembership>, msg: MembershipMsg) -> Result<()> {
    require!(!msg.value.is_empty(), ErrorCode::MembershipEmptyValue);

    let client_state = validate_and_load_client_state(&ctx.accounts.client_state)?;

    let consensus_state_store = validate_and_load_consensus_state(
        &ctx.accounts.consensus_state_at_height,
        ctx.accounts.client_state.key(),
        msg.height,
        ctx.program_id,
    )?;

    validate_proof_params(&client_state, &consensus_state_store, &msg)?;

    let proof = deserialize_merkle_proof(&msg.proof)?;
    let kv_pair = KVPair::new(msg.path, msg.value);
    let app_hash = consensus_state_store.consensus_state.root;

    tendermint_light_client_membership::membership(app_hash, &[(kv_pair, proof)])
        .map_err(|_| error!(ErrorCode::MembershipVerificationFailed))?;

    Ok(())
}

fn validate_and_load_client_state(
    client_state_account: &UncheckedAccount<'_>,
) -> Result<ClientState> {
    let account_data = client_state_account.try_borrow_data()?;
    require!(!account_data.is_empty(), ErrorCode::ClientStateNotFound);

    ClientState::try_deserialize(&mut &account_data[..])
        .map_err(|_| error!(ErrorCode::SerializationError))
}

fn validate_and_load_consensus_state(
    consensus_state_account: &UncheckedAccount<'_>,
    client_key: Pubkey,
    height: u64,
    program_id: &Pubkey,
) -> Result<ConsensusStateStore> {
    // Validate the PDA
    let (expected_pda, _) = Pubkey::find_program_address(
        &[
            b"consensus_state",
            client_key.as_ref(),
            &height.to_le_bytes(),
        ],
        program_id,
    );

    require!(
        expected_pda == consensus_state_account.key(),
        ErrorCode::AccountValidationFailed
    );

    let account_data = consensus_state_account.try_borrow_data()?;
    require!(!account_data.is_empty(), ErrorCode::ConsensusStateNotFound);

    ConsensusStateStore::try_deserialize(&mut &account_data[..])
        .map_err(|_| error!(ErrorCode::SerializationError))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::state::ConsensusStateStore;
    use crate::test_helpers::fixtures::*;
    use crate::types::ClientState;
    use anchor_lang::InstructionData;
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

    fn setup_initialized_client_for_membership() -> TestAccounts {
        // Use the same pattern as update_client: run Initialize instruction to get properly formatted accounts
        let fixture = load_membership_predefined_key_fixture();
        let client_state = client_state_from_fixture(&fixture.client_state);
        let consensus_state = consensus_state_from_fixture(&fixture.consensus_state);
        
        let chain_id = &client_state.chain_id;
        let payer = Pubkey::new_unique();
        let latest_height = client_state.latest_height.revision_height;

        let (client_state_pda, _) =
            Pubkey::find_program_address(&[b"client", chain_id.as_bytes()], &crate::ID);
        let (consensus_state_store_pda, _) = Pubkey::find_program_address(
            &[
                b"consensus_state",
                client_state_pda.as_ref(),
                &latest_height.to_le_bytes(),
            ],
            &crate::ID,
        );

        // Create Initialize instruction
        let instruction_data = crate::instruction::Initialize {
            chain_id: chain_id.to_string(),
            latest_height,
            client_state: client_state.clone(),
            consensus_state: consensus_state.clone(),
        };

        let instruction = Instruction {
            program_id: crate::ID,
            accounts: vec![
                AccountMeta::new(client_state_pda, false),
                AccountMeta::new(consensus_state_store_pda, false),
                AccountMeta::new(payer, true),
                AccountMeta::new_readonly(system_program::ID, false),
            ],
            data: instruction_data.data(),
        };

        // Create empty accounts for Initialize instruction to populate
        let accounts = vec![
            (
                client_state_pda,
                Account {
                    lamports: 0,
                    data: vec![],
                    owner: system_program::ID,
                    executable: false,
                    rent_epoch: 0,
                },
            ),
            (
                consensus_state_store_pda,
                Account {
                    lamports: 0,
                    data: vec![],
                    owner: system_program::ID,
                    executable: false,
                    rent_epoch: 0,
                },
            ),
            (
                payer,
                Account {
                    lamports: 10_000_000_000,
                    data: vec![],
                    owner: system_program::ID,
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

        // Execute Initialize instruction to get properly formatted accounts
        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");
        let result = mollusk.process_instruction(&instruction, &accounts);

        match result.program_result {
            mollusk_svm::result::ProgramResult::Success => {
                // We need to create a consensus state at height 40 to match the membership proof
                let target_height = 40u64;
                let (target_consensus_state_pda, _) = Pubkey::find_program_address(
                    &[
                        b"consensus_state",
                        client_state_pda.as_ref(),
                        &target_height.to_le_bytes(),
                    ],
                    &crate::ID,
                );

                // Create the consensus state at height 40 manually using the same pattern as Initialize
                let mut accounts_with_height_40 = result.resulting_accounts;
                
                // Create the consensus state store for height 40
                let consensus_state_store_40 = ConsensusStateStore {
                    height: target_height,
                    consensus_state: consensus_state.clone(),
                };
                
                let mut consensus_state_data = vec![];
                consensus_state_store_40.try_serialize(&mut consensus_state_data).unwrap();
                
                accounts_with_height_40.push((
                    target_consensus_state_pda,
                    Account {
                        lamports: 1_000_000_000,
                        data: consensus_state_data,
                        owner: crate::ID,
                        executable: false,
                        rent_epoch: 0,
                    },
                ));

                TestAccounts {
                    client_state_pda,
                    consensus_state_store_pda: target_consensus_state_pda,
                    accounts: accounts_with_height_40,
                }
            }
            _ => panic!("Initialize instruction failed: {:?}", result.program_result),
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

        // Convert fixture membership msg to actual MembershipMsg
        let membership_msg = MembershipMsg {
            delay_block_period: fixture.membership_msg.delay_block_period,
            delay_time_period: fixture.membership_msg.delay_time_period,
            height: fixture.membership_msg.height,
            path: fixture
                .membership_msg
                .path
                .iter()
                .map(|s| s.as_bytes().to_vec())
                .collect(),
            proof: hex_to_bytes(&fixture.membership_msg.proof),
            value: hex_to_bytes(&fixture.membership_msg.value),
        };

        let test_accounts = setup_initialized_client_for_membership();

        let instruction = create_verify_membership_instruction(&test_accounts, &membership_msg);

        let mollusk = Mollusk::new(&crate::ID, "../../target/deploy/ics07_tendermint");

        let result = mollusk.process_instruction(&instruction, &test_accounts.accounts);

        match result.program_result {
            mollusk_svm::result::ProgramResult::Success => {
                println!("✅ Membership verification successful for predefined key");
            }
            mollusk_svm::result::ProgramResult::Failure(error) => {
                panic!("❌ Membership verification failed with error: {:?}", error);
            }
            mollusk_svm::result::ProgramResult::UnknownError(error) => {
                panic!(
                    "❌ Membership verification failed with unknown error: {:?}",
                    error
                );
            }
        }
    }
}
