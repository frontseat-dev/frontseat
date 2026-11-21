//! What exists because GitHub hosts the repository: a GitHub release
//! for each unit a release tags.
mod plugin;

frontseat_plugin::serve!(plugin::GitHubPlugin);
