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
    service_ready, Action, ActionKind, Analysis, BranchRequest, BranchResponse, Bump, Code,
    Command, Coordinate, Description, Diagnostic, Download, Duration, Edit, Entity,
    EntityDependencies, EntityRef, EntityRelation, Error, File, GeneratedSource, Handoff, Input,
    InputSet, Origin, Output, OutputRef, Part, Plugin, PluginOption, Printed, Profile, Project,
    Provision, ReleaseCommit, Repository, Requirement, Service, ServiceApi, ServiceCommandReady,
    ServiceHttpReady, ServiceReady, SourceLocation, Value, VersionReason, VersionRequest,
    VersionResponse,
};
