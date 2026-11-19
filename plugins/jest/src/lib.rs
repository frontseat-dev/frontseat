//! Jest suite atoms consuming an installed JavaScript environment.
mod plugin;

frontseat_plugin::serve!(plugin::JestPlugin);
