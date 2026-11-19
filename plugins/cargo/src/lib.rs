//! The Cargo plugin: discovers Rust packages and declares cargo's actions.
mod actions;
mod inputs;
mod lock;
mod manifest;
mod plugin;

frontseat_plugin::serve!(plugin::CargoPlugin);

#[cfg(test)]
mod composition_tests;
