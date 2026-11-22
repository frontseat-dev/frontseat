//! Images built by apko from a configuration and the lock apko wrote of it.
mod actions;
mod config;
mod lock;
mod plugin;

frontseat_plugin::serve!(plugin::ApkoPlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
