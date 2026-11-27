//! Crossplane Configurations: each built into a package, and its examples
//! validated against its own definitions.
mod actions;
mod manifest;
mod plugin;
mod validated;

frontseat_plugin::serve!(plugin::CrossplanePlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
