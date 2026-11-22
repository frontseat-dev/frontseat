//! Native npm dependency preparation, independent of build and test consumers.
mod actions;
mod manifest;
mod plugin;

frontseat_plugin::serve!(plugin::NpmPlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
