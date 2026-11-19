//! Native npm dependency preparation, independent of build and test consumers.
mod actions;
mod manifest;
mod plugin;

frontseat_plugin::serve!(plugin::NpmPlugin);
