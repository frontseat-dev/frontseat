package frontseat

import (
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
)

// The states a caller switches on, named by the SDK, so that a program
// imports no generated package to read where a workspace, a build, a
// run or a release is. Each is the API's own value: GetState() of what a
// call hands back is compared with them.
//
//	if build.GetState() == frontseat.BuildFinished { ... }

// WorkspaceState is where a workspace is.
type WorkspaceState = workspacev1.WorkspaceStatus_State

// Where a workspace is.
const (
	WorkspaceStarting   = workspacev1.WorkspaceStatus_STATE_STARTING
	WorkspacePopulating = workspacev1.WorkspaceStatus_STATE_POPULATING
	WorkspaceReady      = workspacev1.WorkspaceStatus_STATE_READY
	WorkspacePaused     = workspacev1.WorkspaceStatus_STATE_PAUSED
	WorkspaceFailed     = workspacev1.WorkspaceStatus_STATE_FAILED
	WorkspaceEnding     = workspacev1.WorkspaceStatus_STATE_ENDING
)

// BuildState is where a build is.
type BuildState = buildv1.Build_State

// Where a build is.
const (
	BuildRunning   = buildv1.Build_STATE_RUNNING
	BuildFinished  = buildv1.Build_STATE_FINISHED
	BuildCancelled = buildv1.Build_STATE_CANCELLED
)

// RunState is where a run of a program is.
type RunState = buildv1.Run_State

// Where a run of a program is.
const (
	RunBuilding  = buildv1.Run_STATE_BUILDING
	RunRunning   = buildv1.Run_STATE_RUNNING
	RunExited    = buildv1.Run_STATE_EXITED
	RunCancelled = buildv1.Run_STATE_CANCELLED
	RunFailed    = buildv1.Run_STATE_FAILED
)

// ReleaseState is where a release is.
type ReleaseState = buildv1.Release_State

// Where a release is.
const (
	ReleaseBuilding    = buildv1.Release_STATE_BUILDING
	ReleaseTagging     = buildv1.Release_STATE_TAGGING
	ReleasePushing     = buildv1.Release_STATE_PUSHING
	ReleasePublishing  = buildv1.Release_STATE_PUBLISHING
	ReleaseSucceeded   = buildv1.Release_STATE_SUCCEEDED
	ReleaseUnpublished = buildv1.Release_STATE_UNPUBLISHED
	ReleaseFailed      = buildv1.Release_STATE_FAILED
)
