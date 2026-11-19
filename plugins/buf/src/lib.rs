//! Buf workspace discovery and hermetic protobuf generation.
mod actions;
mod manifest;
mod plugin;

frontseat_plugin::serve!(plugin::BufPlugin);
