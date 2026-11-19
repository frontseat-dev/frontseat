//! Release strategies: what a branch releases, and a unit's next version.
//! The host builds the release graph and asks; nothing here reads a file.
mod branching;
mod plugin;
mod versioning;

#[cfg(test)]
mod tests;

frontseat_plugin::serve!(plugin::ReleasePlugin);
