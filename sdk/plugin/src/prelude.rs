//! What a plugin is written with, in one import:
//!
//! ```
//! use frontseat_plugin::prelude::*;
//! ```
//!
//! The trait a plugin implements and what it says of itself; the
//! repository it reads; the contract's messages it answers with; and the
//! constructors every ecosystem plugin uses alike. Modules a plugin uses
//! now and then, `repository`, `entity`, `checksum` and `javascript`,
//! are named where they are used.

pub use crate::diagnostics::{Finding, Located, Unformatted};
pub use crate::{
    service_ready, Action, Analysis, BranchRequest, BranchResponse, Bump, Code, Command,
    Coordinate, Description, Diagnostic, Download, Duration, Edit, Entity, EntityRef,
    EntityRelation, Error, File, GeneratedSource, Handed, Host, Input, InputSet, Origin, Output,
    Part, Plugin, PluginOption, Printed, Produced, Profile, Project, Provide, ReleaseCommit,
    Repository, Requirement, Resolved, Service, ServiceApi, ServiceCommandReady, ServiceHttpReady,
    ServiceReady, SourceLocation, Value, VersionReason, VersionRequest, VersionResponse,
};
