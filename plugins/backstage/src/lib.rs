//! Backstage catalog discovery. Snapshot access and diagnostics come from the SDK.
mod catalog;
mod metadata;
mod plugin;
mod relations;

frontseat_plugin::serve!(plugin::BackstagePlugin);
