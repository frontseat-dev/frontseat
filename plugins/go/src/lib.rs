//! Reference Go plugin: discovers modules and declares Go tool actions.
mod actions;
mod duration;
mod inputs;
mod manifest;
mod plugin;
mod programs;

frontseat_plugin::serve!(plugin::GoPlugin);

#[cfg(test)]
mod composition_tests;

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
