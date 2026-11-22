#![doc = include_str!("../README.md")]

/// The guest half of the ABI and the dispatch of a call to a plugin:
/// what [`serve!`] exports from a module (built for wasm32 alone, so not
/// shown here), and what `frontseat-plugin-test` asks a plugin through.
/// A plugin does not call it.
pub mod abi;

/// The contract's types, generated. A plugin reaches them through the
/// [`prelude`] and the names beside it; these modules are kept for the
/// SDK's own types and for those who want the wire as it is generated.
#[doc(hidden)]
pub mod frontseat {
    pub mod r#type {
        pub mod v1alpha1 {
            include!("gen/frontseat.type.v1alpha1.rs");
        }
    }
    pub mod plugin {
        pub mod v1alpha1 {
            include!("gen/frontseat.plugin.v1alpha1.rs");
        }
    }
}

/// Canonical RPC status codes and details used on every failure boundary.
#[doc(hidden)]
pub mod google {
    pub mod rpc {
        include!("gen/google.rpc.rs");
    }
}

pub use google::rpc::{Code, Status};

pub mod checksum;
pub mod diagnostics;
pub mod javascript;
/// The TOML library the SDK reads TOML with ([`Repository::toml_document`]),
/// for a plugin that names its types: `toml::Spanned`, `toml::Value`.
pub use toml;

/// The contract's messages as they are generated from
/// `api/plugin-proto`: what each call's request and response are on the
/// wire. A plugin is written with the names beside this module, and
/// reaches here for a message that has none.
pub use frontseat::plugin::v1alpha1 as wire;
pub use frontseat::r#type::v1alpha1::{
    option_value, service_ready, Bump, Diagnostic, EntityRef, OptionValue, Profile, Service,
    ServiceApi, ServiceCommandReady, ServiceHttpReady, ServiceReady, SourceLocation,
};
/// A length of time as the contract says one: how long a service is
/// given to be ready (`Service::timeout`).
pub use prost_types::Duration;
pub use wire::action::Kind as ActionKind;
pub use wire::{
    Action, Analysis, BranchRequest, BranchResponse, Command, Coordinate, DependencyInput,
    Download, Edit, Entity, EntityDependencies, EntityRelation, File, GeneratedSource, Handoff,
    Input, InputSet, Origin, Output, OutputRef, Part, Project, Provision, ReleaseCommit,
    Requirement, VersionReason, VersionRequest, VersionResponse,
};
/// What a call is told beside its request, and what the calls answer as
/// the host reads them: what a test of a plugin is answered
/// (`frontseat-plugin-test`).
pub use wire::{
    AnalyzeResponse, ArrangeResponse, Context, DescribeResponse, ReconcileResponse, ReportResponse,
    ServiceResponse,
};
/// The kinds of an action ([`ActionKind`]), each with what only an
/// action of that kind says: what an action's builder sets, and what a
/// test of a plugin reads back.
pub use wire::{Build, Check, Fetch, Package, Program, Publish, SharedFetch, Test};

/// The plugin ABI this SDK speaks.
pub const ABI: u32 = 25;

mod actions;
mod contract;
pub mod entity;
mod inputs;
pub mod prelude;
pub mod repository;
pub mod version;
pub use contract::{Description, Error, Plugin, PluginOption, Printed, Value};
pub use repository::{Host, Repository};

/// Makes a plugin the one its wasm module answers with: in the module,
/// the exports the ABI names; on the machine that builds it, `served()`,
/// the plugin for a test to ask (`frontseat-plugin-test`).
///
/// ```
/// # use frontseat_plugin::prelude::*;
/// struct Nothing;
/// impl Plugin for Nothing {
///     fn describe(&self) -> Description {
///         Description { name: "nothing".into(), ..Default::default() }
///     }
/// }
/// frontseat_plugin::serve!(Nothing);
/// # fn main() { assert_eq!(served().describe().name, "nothing"); }
/// ```
#[macro_export]
macro_rules! serve {
    ($plugin:expr) => {
        /// The plugin, for a test of it on the machine that builds it:
        /// there it is no module, and is asked through
        /// `frontseat-plugin-test`'s `TestPlugin`.
        #[cfg(not(target_arch = "wasm32"))]
        pub fn served() -> impl $crate::Plugin {
            $plugin
        }
        #[cfg(target_arch = "wasm32")]
        #[no_mangle]
        pub extern "C" fn frontseat_abi() -> u32 {
            $crate::ABI
        }
        #[cfg(target_arch = "wasm32")]
        #[no_mangle]
        pub extern "C" fn frontseat_alloc(size: u32) -> u32 {
            $crate::abi::alloc(size)
        }
        #[cfg(target_arch = "wasm32")]
        #[no_mangle]
        pub extern "C" fn frontseat_free(ptr: u32, size: u32) {
            $crate::abi::free(ptr, size)
        }
        #[cfg(target_arch = "wasm32")]
        #[no_mangle]
        pub extern "C" fn frontseat_call(ptr: u32, size: u32) -> u64 {
            $crate::abi::call(&$plugin, ptr, size)
        }
    };
}

#[cfg(test)]
mod tests;
