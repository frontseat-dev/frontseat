//! A Yarn workspace's install: its lock fetched, then installed offline.
mod actions;
mod lock;
mod manifest;
mod plugin;

frontseat_plugin::serve!(plugin::YarnPlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
