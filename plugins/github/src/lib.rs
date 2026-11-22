//! What exists because GitHub hosts the repository: a GitHub release
//! for each unit a release tags.
mod plugin;

frontseat_plugin::serve!(plugin::GitHubPlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
