//! What a frontseat plugin is written against in Rust.
//!
//! The contract between a plugin and the host is the protobuf in
//! `api/plugin-proto`. This crate is a convenience over the types generated
//! from it (`src/gen`, written by `generate/`, never edited): nothing here
//! is on the wire that is not in the contract.
//!
//! A plugin implements [`Plugin`] and hands it to [`serve!`]. What it
//! needs to do so is in the [`prelude`]:
//!
//! ```ignore
//! use frontseat_plugin::prelude::*;
//! ```

#[doc(hidden)]
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

pub use google::rpc::Code;

pub mod checksum;
pub mod diagnostics;
pub mod javascript;

#[doc(hidden)]
pub use frontseat::plugin::v1alpha1 as wire;
pub use frontseat::r#type::v1alpha1::{
    option_value, service_ready, Bump, Diagnostic, EntityRef, OptionValue, Service, ServiceApi,
    ServiceCommandReady, ServiceHttpReady, ServiceReady, SourceLocation,
};
/// A length of time as the contract says one: how long a service is
/// given to be ready (`Service::timeout`).
pub use prost_types::Duration;
pub use wire::{
    Action, Analysis, BranchRequest, BranchResponse, Command, Coordinate, DependencyInput,
    Download, Edit, Entity, EntityRelation, File, GeneratedSource, Handed, Input, InputSet, Origin,
    Output, Part, Produced, Profile, Project, Provide, ReleaseCommit, Requirement, Resolved,
    VersionReason, VersionRequest, VersionResponse,
};

/// The plugin ABI this SDK speaks.
pub const ABI: u32 = 19;

mod actions;
mod contract;
pub mod entity;
mod inputs;
pub mod prelude;
pub mod repository;
pub use contract::{Description, Error, Plugin, PluginOption, Printed, Value};
pub use repository::{Host, Repository};

/// Makes a plugin the one its wasm module answers with.
///
/// ```ignore
/// frontseat_plugin::serve!(GoPlugin);
/// ```
#[macro_export]
macro_rules! serve {
    ($plugin:expr) => {
        /// The plugin, for a test of it on the machine that builds it:
        /// there it is no module, and is asked through
        /// `frontseat_plugin::abi::dispatch` with a host the test supplies.
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
