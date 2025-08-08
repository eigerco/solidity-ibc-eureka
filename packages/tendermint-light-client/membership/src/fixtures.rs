//! Test fixtures for membership verification tests

#![allow(dead_code)]

use serde::Deserialize;
use std::fs;
use std::path::Path;

use crate::KVPair;
use ibc_core_commitment_types::merkle::MerkleProof;

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

/// Membership message fixture structure from JSON
#[derive(Debug, Deserialize)]
pub struct MembershipMsgFixture {
    pub delay_block_period: u64,
    pub delay_time_period: u64,
    pub height: u64,
    pub path: Vec<String>,
    pub proof: String,
    pub value: String,
}

/// Fixture metadata
#[derive(Debug, Deserialize)]
pub struct FixtureMetadata {
    pub description: String,
    pub generated_at: String,
    pub source: String,
}

/// Complete membership verification fixture from JSON
#[derive(Debug, Deserialize)]
pub struct MembershipVerificationFixture {
    pub scenario: String,
    pub client_state: ClientStateFixture,
    pub consensus_state: ConsensusStateFixture,
    pub membership_msg: MembershipMsgFixture,
    pub metadata: FixtureMetadata,
}

impl From<&MembershipMsgFixture> for KVPair {
    fn from(fixture: &MembershipMsgFixture) -> Self {
        let path_bytes: Vec<Vec<u8>> = fixture.path.iter().map(|s| s.as_bytes().to_vec()).collect();
        let value_bytes = hex::decode(&fixture.value).expect("valid hex");
        
        Self::new(path_bytes, value_bytes)
    }
}

/// Convert hex string to MerkleProof (placeholder implementation)
pub fn hex_to_merkle_proof(hex_str: &str) -> MerkleProof {
    let _bytes = hex::decode(hex_str).expect("valid hex");
    // TODO: Implement proper protobuf deserialization for MerkleProof
    // For now, create a minimal proof structure
    MerkleProof {
        proofs: vec![], // This would need proper deserialization
    }
}

/// Load a membership fixture from the fixtures directory
pub fn load_membership_fixture(filename: &str) -> MembershipVerificationFixture {
    let fixture_path = Path::new("../fixtures").join(format!("{}.json", filename));
    let fixture_content = fs::read_to_string(&fixture_path)
        .unwrap_or_else(|_| panic!("Failed to read fixture: {}", fixture_path.display()));

    serde_json::from_str(&fixture_content)
        .unwrap_or_else(|_| panic!("Failed to parse fixture: {}", fixture_path.display()))
}

/// Load the predefined key 0 membership fixture
pub fn load_membership_predefined_key_fixture() -> MembershipVerificationFixture {
    load_membership_fixture("verify_membership_predefined_key_0")
}