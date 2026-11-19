//! Explicit constructors for the protobuf input variants.
use crate::{wire, Coordinate, DependencyInput, Input};

impl Input {
    /// Select source files by pattern, without interpreting reference prefixes.
    pub fn source(pattern: impl Into<String>) -> Self {
        Self {
            kind: Some(wire::input::Kind::Source(pattern.into())),
        }
    }

    /// Select a source input set declared by this project.
    pub fn set(name: impl Into<String>) -> Self {
        Self {
            kind: Some(wire::input::Kind::InputSet(name.into())),
        }
    }

    /// Select a declared output, or an action for all its outputs, without a colon.
    pub fn output(name: impl Into<String>) -> Self {
        Self {
            kind: Some(wire::input::Kind::Output(name.into())),
        }
    }

    /// Read all project requirements' workspace handoffs at this placement.
    pub fn dependencies(into: impl Into<String>) -> Self {
        Self {
            kind: Some(wire::input::Kind::Dependency(DependencyInput {
                selection: Some(wire::dependency_input::Selection::All(true)),
                into: into.into(),
            })),
        }
    }

    /// Read one declared requirement's workspace handoffs at this placement.
    pub fn dependency(coordinate: Coordinate, into: impl Into<String>) -> Self {
        Self {
            kind: Some(wire::input::Kind::Dependency(DependencyInput {
                selection: Some(wire::dependency_input::Selection::Coordinate(coordinate)),
                into: into.into(),
            })),
        }
    }
}
