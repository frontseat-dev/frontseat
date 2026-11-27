//! Helm charts: each directory that holds a Chart.yaml, linted and
//! rendered by Helm.
mod actions;
mod chart;
mod plugin;

frontseat_plugin::serve!(plugin::HelmPlugin);

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
