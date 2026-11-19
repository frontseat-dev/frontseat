//! Jest suites, each one piece of tests, consuming an installed JavaScript environment.
mod plugin;

frontseat_plugin::serve!(plugin::JestPlugin);
