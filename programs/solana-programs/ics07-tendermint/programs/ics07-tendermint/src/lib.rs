use anchor_lang::prelude::*;

declare_id!("4jnHS7kRa7V4a4G8BU6F7FYsiQeqsJheuoGCBGGHqsXN");

#[program]
pub mod ics07_tendermint {
    use super::*;

    pub fn initialize(ctx: Context<Initialize>) -> Result<()> {
        msg!("Greetings from: {:?}", ctx.program_id);
        Ok(())
    }
}

#[derive(Accounts)]
pub struct Initialize {}
