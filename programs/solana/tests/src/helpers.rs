use anchor_client::solana_client::rpc_client::RpcClient;
use anchor_client::solana_sdk::{
    commitment_config::CommitmentConfig,
    native_token::LAMPORTS_PER_SOL,
    pubkey::Pubkey,
    signature::{Keypair, Signer},
};
use anchor_client::{Client, Cluster, Program};
use base64::prelude::*;
use ibc_proto::ibc::core::commitment::v1::MerkleProof as RawMerkleProof;
use ibc_proto::ibc::lightclients::tendermint::v1::Header as RawHeader;
use ibc_proto::ibc::lightclients::tendermint::v1::Misbehaviour as RawMisbehaviour;
use ics07_tendermint::types::IbcHeight;
use ics07_tendermint::{ClientState, ConsensusState, MembershipMsg};
use prost::Message;
use serde::{Deserialize, Serialize};
use solana_system_interface::program as system_program;
use std::rc::Rc;
use std::time::{SystemTime, UNIX_EPOCH};

pub struct TestEnv {
    pub payer: Rc<Keypair>,
    pub client: Client<Rc<Keypair>>,
    pub program: Program<Rc<Keypair>>,
}

pub fn setup_test_env(program_id: Pubkey) -> TestEnv {
    let payer = Rc::new(Keypair::new());

    let client = Client::new_with_options(
        Cluster::Localnet,
        payer.clone(),
        CommitmentConfig::confirmed(),
    );

    let rpc = RpcClient::new_with_commitment(
        Cluster::Localnet.url().to_string(),
        CommitmentConfig::confirmed(),
    );

    let program = client.program(program_id).expect("Failed to get program");

    let env = TestEnv {
        payer,
        client,
        program,
    };

    let airdrop_sig = request_airdrop(&env, &rpc, 2 * LAMPORTS_PER_SOL);
    wait_for_airdrop_confirmation(&env, &rpc, &airdrop_sig, 30);

    env
}

pub fn log(setup: &TestEnv, message: &str) {
    println!("[payer: {}] {}", setup.payer.pubkey(), message);
}

fn request_airdrop(
    env: &TestEnv,
    rpc: &RpcClient,
    amount: u64,
) -> anchor_client::solana_sdk::signature::Signature {
    log(env, &format!("💰 Requesting Airdrop - amount: {}", amount));

    let signature = rpc
        .request_airdrop(&env.payer.pubkey(), amount)
        .expect("Failed to request airdrop");

    log(env, &format!("💰 Airdrop requested - sig: {}", signature));

    signature
}

fn wait_for_airdrop_confirmation(
    env: &TestEnv,
    rpc: &RpcClient,
    airdrop_sig: &anchor_client::solana_sdk::signature::Signature,
    max_attempts: u64,
) {
    let mut attempts = 0;
    while attempts < max_attempts {
        match rpc.confirm_transaction(airdrop_sig) {
            Ok(true) => {
                log(env, &format!("✅ Airdrop confirmed - sig: {}", airdrop_sig));
                return;
            }
            Ok(false) | Err(_) => {
                attempts += 1;
                std::thread::sleep(std::time::Duration::from_secs(1));
            }
        }
    }
    panic!(
        "Airdrop confirmation timeout after {} seconds",
        max_attempts
    );
}

pub struct InitializedContract {
    pub client_data_pda: Pubkey,
    pub client_state: ClientState,
    pub consensus_state: ConsensusState,
}

pub fn generate_unique_chain_id() -> String {
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_nanos();
    format!("test-chain-{}", timestamp)
}

pub fn initialize_contract(
    env: &TestEnv,
    program_id: Pubkey,
    client_state: ClientState,
    consensus_state: ConsensusState,
) -> InitializedContract {
    let (client_data_pda, _bump) =
        Pubkey::find_program_address(&[b"client", client_state.chain_id.as_bytes()], &program_id);

    log(
        env,
        &format!(
            "🚀 Initializing contract with chain_id: {}",
            client_state.chain_id
        ),
    );
    log(env, &format!("📍 Client data PDA: {}", client_data_pda));

    // Calculate the consensus state store PDA
    // Use the client state's latest height - this allows initializing at any blockchain height
    let consensus_height = client_state.latest_height.revision_height;
    let (consensus_state_store_pda, _bump) = Pubkey::find_program_address(
        &[
            b"consensus_state",
            client_data_pda.as_ref(),
            &consensus_height.to_le_bytes(),
        ],
        &env.program.id(),
    );

    let chain_id = client_state.chain_id.clone();

    log(env, &format!("🔍 Chain ID: {}", chain_id));
    log(
        env,
        &format!("🔍 Chain ID bytes: {:?}", chain_id.as_bytes()),
    );
    log(
        env,
        &format!(
            "🔍 Instruction client_state height: {}",
            client_state.latest_height.revision_height
        ),
    );
    log(
        env,
        &format!(
            "🔍 Height bytes: {:?}",
            client_state.latest_height.revision_height.to_le_bytes()
        ),
    );
    log(
        env,
        &format!(
            "🔍 consensus_state_store PDA: {}",
            consensus_state_store_pda
        ),
    );

    // Build and send the initialize instruction
    let instruction = env
        .program
        .request()
        .args(ics07_tendermint::instruction::Initialize {
            chain_id: chain_id,
            client_state: client_state.clone(),
            latest_height: consensus_height, // Initialize at the client state's latest height
            consensus_state: consensus_state.clone(),
        })
        .accounts(ics07_tendermint::accounts::Initialize {
            client_state: client_data_pda,
            consensus_state_store: consensus_state_store_pda,
            payer: env.payer.pubkey(),
            system_program: system_program::ID,
        })
        .instructions()
        .expect("Failed to build instruction");

    let signature = env
        .program
        .request()
        .instruction(instruction[0].clone())
        .signer(env.payer.as_ref())
        .send()
        .expect("Failed to initialize contract");

    log(env, &format!("✅ Contract initialized - tx: {}", signature));

    InitializedContract {
        client_data_pda,
        client_state,
        consensus_state,
    }
}

/// Creates a minimal valid protobuf-encoded Header for testing
pub fn create_test_header_bytes() -> Vec<u8> {
    // Create a minimal RawHeader that will pass basic validation
    let raw_header = RawHeader {
        signed_header: Some(Default::default()),
        validator_set: Some(Default::default()),
        trusted_height: Some(ibc_proto::ibc::core::client::v1::Height {
            revision_number: 0,
            revision_height: 1,
        }),
        trusted_validators: Some(Default::default()),
    };

    // Encode to protobuf bytes
    let mut buf = Vec::new();
    raw_header
        .encode(&mut buf)
        .expect("encoding should succeed");
    buf
}

/// Creates a minimal valid protobuf-encoded MerkleProof for testing
pub fn create_test_merkle_proof_bytes() -> Vec<u8> {
    // Create a minimal RawMerkleProof
    let raw_proof = RawMerkleProof { proofs: vec![] };

    // Encode to protobuf bytes
    let mut buf = Vec::new();
    raw_proof.encode(&mut buf).expect("encoding should succeed");
    buf
}

/// Creates a minimal valid protobuf-encoded Misbehaviour for testing
pub fn create_test_misbehaviour_bytes() -> Vec<u8> {
    // Create a minimal RawMisbehaviour with two headers
    let raw_misbehaviour = RawMisbehaviour {
        client_id: "test-client".to_string(),
        header_1: Some(Default::default()),
        header_2: Some(Default::default()),
    };

    // Encode to protobuf bytes
    let mut buf = Vec::new();
    raw_misbehaviour
        .encode(&mut buf)
        .expect("encoding should succeed");
    buf
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct ClientStateFixture {
    chain_id: String,
    trust_level_numerator: u64,
    trust_level_denominator: u64,
    trusting_period: u64,
    unbonding_period: u64,
    max_clock_drift: u64,
    frozen_height: u64,
    latest_height: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct ConsensusStateFixture {
    timestamp: u64,
    root: String,                 // hex string
    next_validators_hash: String, // hex string
}

#[derive(Debug, Clone, Deserialize)]
struct UpdateClientMessageFixture {
    client_message_bytes: String, // This is base64-encoded
}

/// Loads client state from fixture file
pub fn load_client_state_from_fixture() -> ClientState {
    let fixture_path = "fixtures/client_state.json";
    let fixture_content =
        std::fs::read_to_string(fixture_path).expect("Failed to read client state fixture");

    let fixture: ClientStateFixture =
        serde_json::from_str(&fixture_content).expect("Failed to parse client state fixture");

    ClientState {
        chain_id: fixture.chain_id,
        trust_level_numerator: fixture.trust_level_numerator,
        trust_level_denominator: fixture.trust_level_denominator,
        trusting_period: fixture.trusting_period,
        unbonding_period: fixture.unbonding_period,
        max_clock_drift: fixture.max_clock_drift,
        frozen_height: ics07_tendermint::types::IbcHeight {
            revision_number: 0,
            revision_height: fixture.frozen_height,
        },
        latest_height: ics07_tendermint::types::IbcHeight {
            revision_number: 0,
            revision_height: fixture.latest_height,
        },
    }
}

/// Loads consensus state from fixture file
pub fn load_consensus_state_from_fixture() -> ConsensusState {
    let fixture_path = "fixtures/consensus_state.json";
    let fixture_content =
        std::fs::read_to_string(fixture_path).expect("Failed to read consensus state fixture");

    let fixture: ConsensusStateFixture =
        serde_json::from_str(&fixture_content).expect("Failed to parse consensus state fixture");

    // Convert hex strings to byte arrays
    let root = hex::decode(&fixture.root)
        .expect("Failed to decode root hex")
        .try_into()
        .expect("Root must be 32 bytes");

    let next_validators_hash = hex::decode(&fixture.next_validators_hash)
        .expect("Failed to decode next_validators_hash hex")
        .try_into()
        .expect("Next validators hash must be 32 bytes");

    ConsensusState {
        timestamp: fixture.timestamp,
        root,
        next_validators_hash,
    }
}

/// Loads update client message from fixture file
pub fn load_update_client_message_from_fixture() -> Vec<u8> {
    let fixture_path = "fixtures/update_client_message.json";
    let fixture_content = std::fs::read_to_string(fixture_path)
        .expect("Failed to read update client message fixture");

    let fixture: UpdateClientMessageFixture = serde_json::from_str(&fixture_content)
        .expect("Failed to parse update client message fixture");

    // Decode the base64 string to bytes
    BASE64_STANDARD
        .decode(&fixture.client_message_bytes)
        .expect("Failed to decode base64 client message bytes")
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct MembershipFixture {
    client_state: ClientStateFixture,
    consensus_state: ConsensusStateFixture,
    membership_msg: MembershipMsgFixture,
    expected_result: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct MembershipMsgFixture {
    height: u64,
    delay_time_period: u64,
    delay_block_period: u64,
    proof: String, // hex string
    path: String,  // hex string
    value: String, // hex string
}

/// Loads membership message from fixture file
pub fn load_membership_fixture_from_file(
    fixture_index: usize,
) -> (ClientState, ConsensusState, MembershipMsg) {
    let fixture_path = format!("fixtures/membership_{}.json", fixture_index);
    let fixture_content = std::fs::read_to_string(&fixture_path).expect(&format!(
        "Failed to read membership fixture: {}",
        fixture_path
    ));

    let fixture: MembershipFixture =
        serde_json::from_str(&fixture_content).expect("Failed to parse membership fixture");

    // Debug the fixture values before decoding
    println!("🔍 Debug: Raw fixture values:");
    println!("   - Raw value string: '{}'", fixture.membership_msg.value);
    println!("   - Raw path string: '{}'", fixture.membership_msg.path);
    println!("   - Raw proof length: {}", fixture.membership_msg.proof.len());

    let client_state = ClientState {
        chain_id: fixture.client_state.chain_id,
        trust_level_numerator: fixture.client_state.trust_level_numerator,
        trust_level_denominator: fixture.client_state.trust_level_denominator,
        trusting_period: fixture.client_state.trusting_period,
        unbonding_period: fixture.client_state.unbonding_period,
        max_clock_drift: fixture.client_state.max_clock_drift,
        frozen_height: IbcHeight {
            revision_number: 0,
            revision_height: fixture.client_state.frozen_height,
        },
        latest_height: IbcHeight {
            revision_number: 0,
            revision_height: fixture.client_state.latest_height,
        },
    };

    let consensus_state = ConsensusState {
        timestamp: fixture.consensus_state.timestamp,
        root: hex::decode(&fixture.consensus_state.root)
            .expect("Failed to decode root hex")
            .try_into()
            .expect("Root must be 32 bytes"),
        next_validators_hash: hex::decode(&fixture.consensus_state.next_validators_hash)
            .expect("Failed to decode next_validators_hash hex")
            .try_into()
            .expect("Next validators hash must be 32 bytes"),
    };

    let membership_msg = MembershipMsg {
        height: fixture.membership_msg.height,
        delay_time_period: fixture.membership_msg.delay_time_period,
        delay_block_period: fixture.membership_msg.delay_block_period,
        proof: hex::decode(&fixture.membership_msg.proof).expect("Failed to decode proof hex"),
        path: {
            let path_bytes =
                hex::decode(&fixture.membership_msg.path).expect("Failed to decode path hex");
            // Cosmos SDK merkle path: [store_key, substore_key] - matches Solidity test format
            vec![b"ibc".to_vec(), path_bytes]
        },
        value: hex::decode(&fixture.membership_msg.value).expect("Failed to decode value hex"),
    };

    (client_state, consensus_state, membership_msg)
}

/// Loads non-membership message from fixture file
pub fn load_non_membership_fixture_from_file(
    fixture_index: usize,
) -> (ClientState, ConsensusState, MembershipMsg) {
    let fixture_path = format!("fixtures/non_membership_{}.json", fixture_index);
    let fixture_content = std::fs::read_to_string(&fixture_path).expect(&format!(
        "Failed to read non-membership fixture: {}",
        fixture_path
    ));

    let fixture: MembershipFixture =
        serde_json::from_str(&fixture_content).expect("Failed to parse non-membership fixture");

    let client_state = ClientState {
        chain_id: fixture.client_state.chain_id,
        trust_level_numerator: fixture.client_state.trust_level_numerator,
        trust_level_denominator: fixture.client_state.trust_level_denominator,
        trusting_period: fixture.client_state.trusting_period,
        unbonding_period: fixture.client_state.unbonding_period,
        max_clock_drift: fixture.client_state.max_clock_drift,
        frozen_height: IbcHeight {
            revision_number: 0,
            revision_height: fixture.client_state.frozen_height,
        },
        latest_height: IbcHeight {
            revision_number: 0,
            revision_height: fixture.client_state.latest_height,
        },
    };

    let consensus_state = ConsensusState {
        timestamp: fixture.consensus_state.timestamp,
        root: hex::decode(&fixture.consensus_state.root)
            .expect("Failed to decode root hex")
            .try_into()
            .expect("Root must be 32 bytes"),
        next_validators_hash: hex::decode(&fixture.consensus_state.next_validators_hash)
            .expect("Failed to decode next_validators_hash hex")
            .try_into()
            .expect("Next validators hash must be 32 bytes"),
    };

    let membership_msg = MembershipMsg {
        height: fixture.membership_msg.height,
        delay_time_period: fixture.membership_msg.delay_time_period,
        delay_block_period: fixture.membership_msg.delay_block_period,
        proof: hex::decode(&fixture.membership_msg.proof).expect("Failed to decode proof hex"),
        path: {
            let path_bytes =
                hex::decode(&fixture.membership_msg.path).expect("Failed to decode path hex");
            // Cosmos SDK merkle path: [store_key, substore_key] - matches Solidity test format
            vec![b"ibc".to_vec(), path_bytes]
        },
        value: hex::decode(&fixture.membership_msg.value).expect("Failed to decode value hex"),
    };

    (client_state, consensus_state, membership_msg)
}
