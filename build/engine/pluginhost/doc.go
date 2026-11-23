// Package pluginhost loads wasm plugins and asks them the contract's
// PluginService. It serves each plugin the contract's HostService out of
// a Source, records what the plugin read, and checks every answer before
// anything else sees it. It knows the plugin ABI and nothing of what an
// answer is used for.
package pluginhost
