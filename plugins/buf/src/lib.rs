//! Buf workspace discovery and hermetic protobuf generation.
mod actions;
mod manifest;
mod plugin;

frontseat_plugin::serve!(plugin::BufPlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
