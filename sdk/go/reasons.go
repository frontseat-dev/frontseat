package frontseat

// The reasons a server refuses a call by name, as an Error's Reason says
// them. Each call's contract lists the reasons it gives, and when; the
// architecture test holds this list to what the servers give.
const (
	// ReasonActionNotFound: a build has planned no action of the label.
	ReasonActionNotFound = "ACTION_NOT_FOUND"
	// ReasonBuildNotFound: no build has the id.
	ReasonBuildNotFound = "BUILD_NOT_FOUND"
	// ReasonConflictingOwnership: two entities own one directory.
	ReasonConflictingOwnership = "CONFLICTING_OWNERSHIP"
	// ReasonCredentialHostInvalid: a credential's host is not one.
	ReasonCredentialHostInvalid = "CREDENTIAL_HOST_INVALID"
	// ReasonCredentialNotKept: what keeps credentials outside the
	// sandboxes could not keep or let go of the credential.
	ReasonCredentialNotKept = "CREDENTIAL_NOT_KEPT"
	// ReasonCredentialsNotServed: the control plane's sandboxes keep no
	// credential outside them.
	ReasonCredentialsNotServed = "CREDENTIALS_NOT_SERVED"
	// ReasonCredentialInUse: a workspace is let through with the
	// credential, which is not let go of.
	ReasonCredentialInUse = "CREDENTIAL_IN_USE"
	// ReasonCredentialSecretInvalid: a credential's secret is not one.
	ReasonCredentialSecretInvalid = "CREDENTIAL_SECRET_INVALID"
	// ReasonCredentialUsernameInvalid: a credential's username is not
	// one.
	ReasonCredentialUsernameInvalid = "CREDENTIAL_USERNAME_INVALID"
	// ReasonDaemonEnding: the build daemon is ending.
	ReasonDaemonEnding = "DAEMON_ENDING"
	// ReasonDuplicateEntity: an entity is described twice.
	ReasonDuplicateEntity = "DUPLICATE_ENTITY"
	// ReasonEntitiesNotServed: the control plane has no catalog to
	// resolve entities through.
	ReasonEntitiesNotServed = "ENTITIES_NOT_SERVED"
	// ReasonEntitiesUnavailable: the catalog did not answer as one does.
	ReasonEntitiesUnavailable = "ENTITIES_UNAVAILABLE"
	// ReasonEntityAmbiguous: a name names several entities.
	ReasonEntityAmbiguous = "ENTITY_AMBIGUOUS"
	// ReasonEntityInvalid: an entity is no entity's name, or the catalog
	// says of it what cannot be held.
	ReasonEntityInvalid = "ENTITY_INVALID"
	// ReasonEntityNotFound: no entity has the name.
	ReasonEntityNotFound = "ENTITY_NOT_FOUND"
	// ReasonEtagMismatch: a release plan is no longer what releasing the
	// same entities would do now; plan again.
	ReasonEtagMismatch = "ETAG_MISMATCH"
	// ReasonEtagMissing: a release is asked for with no plan's etag.
	ReasonEtagMissing = "ETAG_MISSING"
	// ReasonFetchNotServed: a repository is to be fetched and the
	// control plane was told of no version control system.
	ReasonFetchNotServed = "FETCH_NOT_SERVED"
	// ReasonGeneratedWithoutConsent: what is generated is beyond what its
	// Component may write.
	ReasonGeneratedWithoutConsent = "GENERATED_WITHOUT_CONSENT"
	// ReasonGenerationInvalid: the workspace's supervisor was told what
	// to hold at a generation it has passed.
	ReasonGenerationInvalid = "GENERATION_INVALID"
	// ReasonIdentityInvalid: the caller's token is not one the control
	// plane knows.
	ReasonIdentityInvalid = "IDENTITY_INVALID"
	// ReasonIdentityMissing: the caller presented no token.
	ReasonIdentityMissing = "IDENTITY_MISSING"
	// ReasonImageNotServed: no image is named, and the control plane
	// names none of its own.
	ReasonImageNotServed = "IMAGE_NOT_SERVED"
	// ReasonLifetimeInvalid: a workspace's lifetime is not one.
	ReasonLifetimeInvalid = "LIFETIME_INVALID"
	// ReasonLogNotKept: the build daemon no longer keeps what an action
	// printed.
	ReasonLogNotKept = "LOG_NOT_KEPT"
	// ReasonNameInvalid: a workspace's name is not one.
	ReasonNameInvalid = "NAME_INVALID"
	// ReasonNotOwner: the workspace is another's.
	ReasonNotOwner = "NOT_OWNER"
	// ReasonPageTokenInvalid: a List was given a page token it did not
	// give.
	ReasonPageTokenInvalid = "PAGE_TOKEN_INVALID"
	// ReasonPortNotServed: the control plane carries no one to the port
	// asked for of a workspace's sandbox: it is the port of the
	// supervisor's source API, which is the control plane's own, or it is
	// no port.
	ReasonPortNotServed = "PORT_NOT_SERVED"
	// ReasonPortUnreached: the port asked for of a workspace's sandbox
	// was not reached: nothing answers there, or the sandbox is not
	// running.
	ReasonPortUnreached = "PORT_UNREACHED"
	// ReasonProfileUnknown: the workspace defines no profile of the name.
	ReasonProfileUnknown = "PROFILE_UNKNOWN"
	// ReasonPushNotServed: a workspace that may push is asked of a
	// control plane that makes none.
	ReasonPushNotServed = "PUSH_NOT_SERVED"
	// ReasonPublishNotServed: a release from a workspace would publish
	// something, which a workspace's sandbox cannot.
	ReasonPublishNotServed = "PUBLISH_NOT_SERVED"
	// ReasonReachNotServed: a repository is beyond what the workspace's
	// sandbox may reach.
	ReasonReachNotServed = "REACH_NOT_SERVED"
	// ReasonReleaseBusy: another release is being made.
	ReasonReleaseBusy = "RELEASE_BUSY"
	// ReasonReleaseInvalid: what a release is asked for cannot be
	// released.
	ReasonReleaseInvalid = "RELEASE_INVALID"
	// ReasonReleaseNotCancellable: a release is tagging, pushing or
	// publishing, and is carried through.
	ReasonReleaseNotCancellable = "RELEASE_NOT_CANCELLABLE"
	// ReasonReleaseNotFound: no release has the id.
	ReasonReleaseNotFound = "RELEASE_NOT_FOUND"
	// ReasonReleaseNotServed: the build daemon releases nothing.
	ReasonReleaseNotServed = "RELEASE_NOT_SERVED"
	// ReasonRemoteCredentialed: a repository's address holds a user or a
	// password.
	ReasonRemoteCredentialed = "REMOTE_CREDENTIALED"
	// ReasonRequestIDInvalid: a request id is longer than one may be.
	ReasonRequestIDInvalid = "REQUEST_ID_INVALID"
	// ReasonRunsExhausted: the build daemon has as many runs that have
	// not ended as it keeps.
	ReasonRunsExhausted = "RUNS_EXHAUSTED"
	// ReasonRunNotFound: no run has the id.
	ReasonRunNotFound = "RUN_NOT_FOUND"
	// ReasonSequenceOutOfRange: the build has ended, and the sequence is
	// beyond its last event.
	ReasonSequenceOutOfRange = "SEQUENCE_OUT_OF_RANGE"
	// ReasonSourceMissing: what the workspace is to hold is not there.
	ReasonSourceMissing = "SOURCE_MISSING"
	// ReasonSourceMistaken: the workspace's source cannot be read as a
	// workspace.
	ReasonSourceMistaken = "SOURCE_MISTAKEN"
	// ReasonSourceMounted: the workspace's source is mounted, and is not
	// cloned into.
	ReasonSourceMounted = "SOURCE_MOUNTED"
	// ReasonSourcePopulating: the workspace's supervisor is still
	// bringing what it was told before.
	ReasonSourcePopulating = "SOURCE_POPULATING"
	// ReasonSourceUnfit: what the workspace is to hold cannot be held.
	ReasonSourceUnfit = "SOURCE_UNFIT"
	// ReasonSpecFixed: a change to what is said of a workspace once.
	ReasonSpecFixed = "SPEC_FIXED"
	// ReasonSpecIncomplete: a spec leaves out something the workspace
	// has.
	ReasonSpecIncomplete = "SPEC_INCOMPLETE"
	// ReasonSpecInvalid: a spec is not written as one is.
	ReasonSpecInvalid = "SPEC_INVALID"
	// ReasonStoreNotServed: a remote workspace, one that holds stored
	// repositories, works on a task or lands, is asked of a control plane
	// that was given nothing that serves stored repositories: Frontseat
	// Enterprise serves them.
	ReasonStoreNotServed = "STORE_NOT_SERVED"
	// ReasonStoreUnavailable: what serves stored repositories did not
	// hold those a workspace was asked for; nothing was made.
	ReasonStoreUnavailable = "STORE_UNAVAILABLE"
	// ReasonTargetMissing: a run names no program.
	ReasonTargetMissing = "TARGET_MISSING"
	// ReasonTunnelsExhausted: the workspace is carried as many
	// connections, to its shell and the ports of its sandbox, as one is
	// at once.
	ReasonTunnelsExhausted = "TUNNELS_EXHAUSTED"
	// ReasonWorkspaceDiffers: a workspace of the name is there and is
	// asked for otherwise.
	ReasonWorkspaceDiffers = "WORKSPACE_DIFFERS"
	// ReasonWorkspaceEnding: the workspace is ending.
	ReasonWorkspaceEnding = "WORKSPACE_ENDING"
	// ReasonWorkspaceNotFound: no workspace of the caller's has the name.
	ReasonWorkspaceNotFound = "WORKSPACE_NOT_FOUND"
	// ReasonWorkspaceNotReady: the workspace cannot be worked in yet:
	// starting, bringing its source, paused or failed.
	ReasonWorkspaceNotReady = "WORKSPACE_NOT_READY"
)

// The refusals a caller is most likely to act on, for errors.Is: each
// matches an Error of its reason, in its domain where it names one.
var (
	// ErrWorkspaceNotFound: no workspace of the caller's has the name.
	ErrWorkspaceNotFound = &Error{Reason: ReasonWorkspaceNotFound, Domain: DomainWorkspace}
	// ErrWorkspaceNotReady: the workspace cannot be worked in yet. The
	// SDK refuses so too, before a build daemon is asked, a workspace
	// that Open or Add waited for and found otherwise than ready, and
	// one whose build daemon is asked before it has one.
	ErrWorkspaceNotReady = &Error{Reason: ReasonWorkspaceNotReady}
	// ErrBuildNotFound: no build has the id.
	ErrBuildNotFound = &Error{Reason: ReasonBuildNotFound, Domain: DomainBuild}
	// ErrRunNotFound: no run has the id.
	ErrRunNotFound = &Error{Reason: ReasonRunNotFound, Domain: DomainBuild}
	// ErrReleaseNotFound: no release has the id.
	ErrReleaseNotFound = &Error{Reason: ReasonReleaseNotFound, Domain: DomainBuild}
	// ErrEtagMismatch: the release plan is no longer what releasing the
	// same entities would do now; plan again.
	ErrEtagMismatch = &Error{Reason: ReasonEtagMismatch, Domain: DomainBuild}
	// ErrEntitiesNotServed: the control plane has no catalog to resolve
	// entities through.
	ErrEntitiesNotServed = &Error{Reason: ReasonEntitiesNotServed, Domain: DomainWorkspace}
)
