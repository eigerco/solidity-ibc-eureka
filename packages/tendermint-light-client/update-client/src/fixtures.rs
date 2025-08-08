//! Test fixtures for update client tests

#![allow(dead_code)]

use serde::Deserialize;
use std::fs;
use std::path::Path;

use crate::{ClientState, TrustThreshold};
use ibc_core_client_types::Height;
use ibc_client_tendermint::types::{ConsensusState, Header};
use ibc_core_commitment_types::commitment::CommitmentRoot;
use prost::Message;

/// Client state fixture structure from JSON
#[derive(Debug, Deserialize)]
pub struct ClientStateFixture {
    pub chain_id: String,
    pub frozen_height: u64,
    pub latest_height: u64,
    pub max_clock_drift: u64,
    pub trust_level_denominator: u32,
    pub trust_level_numerator: u32,
    pub trusting_period: u64,
    pub unbonding_period: u64,
}

/// Consensus state fixture structure from JSON
#[derive(Debug, Deserialize)]
pub struct ConsensusStateFixture {
    pub next_validators_hash: String,
    pub root: String,
    pub timestamp: u64,
}

/// Update client message fixture structure from JSON
#[derive(Debug, Deserialize, Clone)]
pub struct UpdateClientMessageFixture {
    pub client_message_hex: String,
    pub type_url: String,
    pub trusted_height: u64,
    pub new_height: u64,
}

/// Fixture metadata
#[derive(Debug, Deserialize)]
pub struct FixtureMetadata {
    pub description: String,
    pub generated_at: String,
    pub source: String,
}

/// Complete update client fixture from JSON
#[derive(Debug, Deserialize)]
pub struct UpdateClientFixture {
    pub scenario: String,
    pub client_state: ClientStateFixture,
    pub trusted_consensus_state: ConsensusStateFixture,
    pub update_client_message: UpdateClientMessageFixture,
    pub metadata: FixtureMetadata,
}

impl From<&ClientStateFixture> for ClientState {
    fn from(fixture: &ClientStateFixture) -> Self {
        Self {
            chain_id: fixture.chain_id.clone(),
            trust_level: TrustThreshold::new(
                fixture.trust_level_numerator as u64,
                fixture.trust_level_denominator as u64,
            ),
            trusting_period_seconds: fixture.trusting_period,
            unbonding_period_seconds: fixture.unbonding_period,
            max_clock_drift_seconds: fixture.max_clock_drift,
            is_frozen: fixture.frozen_height > 0,
            latest_height: Height::new(0, fixture.latest_height).expect("valid height"),
        }
    }
}

/// Create a consensus state from fixture (simplified for testing)
pub fn consensus_state_from_fixture(fixture: &ConsensusStateFixture) -> Result<ConsensusState, Box<dyn std::error::Error>> {
    let root_bytes = hex::decode(&fixture.root).map_err(|e| format!("Failed to decode root hex: {}", e))?;
    let next_validators_hash_bytes = hex::decode(&fixture.next_validators_hash).map_err(|e| format!("Failed to decode next_validators_hash hex: {}", e))?;
    
    let timestamp = tendermint::Time::from_unix_timestamp(
        (fixture.timestamp / 1_000_000_000) as i64,
        (fixture.timestamp % 1_000_000_000) as u32,
    ).map_err(|e| format!("Failed to create timestamp: {}", e))?;
    
    let next_validators_hash = tendermint::Hash::from_bytes(
        tendermint::hash::Algorithm::Sha256,
        &next_validators_hash_bytes
    ).map_err(|e| format!("Failed to create next_validators_hash: {}", e))?;
    
    // Create consensus state with proper constructor
    let commitment_root = CommitmentRoot::from_bytes(&root_bytes);
    
    Ok(ConsensusState::new(
        commitment_root,
        timestamp,
        next_validators_hash,
    ))
}

/// Load a fixture from the fixtures directory
pub fn load_fixture(filename: &str) -> UpdateClientFixture {
    let fixture_path = Path::new("../fixtures").join(format!("{}.json", filename));
    let fixture_content = fs::read_to_string(&fixture_path)
        .unwrap_or_else(|_| panic!("Failed to read fixture: {}", fixture_path.display()));

    serde_json::from_str(&fixture_content)
        .unwrap_or_else(|_| panic!("Failed to parse fixture: {}", fixture_path.display()))
}

/// Load the happy path fixture
pub fn load_happy_path_fixture() -> UpdateClientFixture {
    load_fixture("update_client_happy_path")
}

/// Load the malformed client message fixture
pub fn load_malformed_client_message_fixture() -> UpdateClientFixture {
    load_fixture("update_client_malformed_client_message")
}

/// Load the expired header fixture
pub fn load_expired_header_fixture() -> UpdateClientFixture {
    load_fixture("update_client_expired_header")
}

/// Load the future timestamp fixture
pub fn load_future_timestamp_fixture() -> UpdateClientFixture {
    load_fixture("update_client_future_timestamp")
}


/// Load the invalid protobuf fixture
pub fn load_invalid_protobuf_fixture() -> UpdateClientFixture {
    load_fixture("update_client_invalid_protobuf")
}

/// Convert hex string to bytes (placeholder for Header conversion)
pub fn hex_to_bytes(hex_str: &str) -> Vec<u8> {
    hex::decode(hex_str).expect("valid hex")
}

/// Convert hex string to Header by deserializing protobuf bytes
pub fn hex_to_header(hex_str: &str) -> Result<Header, Box<dyn std::error::Error>> {
    let bytes = hex::decode(hex_str).map_err(|e| format!("Failed to decode header hex: {}", e))?;
    
    // Try to deserialize as a tendermint Header protobuf
    let proto_header = ibc_client_tendermint::types::proto::v1::Header::decode(&bytes[..])
        .map_err(|e| format!("Failed to decode protobuf header: {}", e))?;
    
    // Convert to the IBC Header type
    let header = Header::try_from(proto_header)
        .map_err(|e| format!("Failed to convert header: {}", e))?;
    
    Ok(header)
}