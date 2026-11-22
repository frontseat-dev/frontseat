//! Jest suites, each one piece of tests, consuming an installed JavaScript environment.
mod plugin;

frontseat_plugin::serve!(plugin::JestPlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
