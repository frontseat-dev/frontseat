//! Backstage catalog discovery, and the packages the Backstage CLI builds.
//! Snapshot access and diagnostics come from the SDK.
mod catalog;
mod metadata;
mod packages;
mod plugin;
mod relations;

frontseat_plugin::serve!(plugin::BackstagePlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
