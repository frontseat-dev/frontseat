package pluginhost_test

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
)

// releaseFixture is what the release plugin is asked and must answer,
// spelled out independently of the Rust: Release Flow, where a branch
// release/<major>.<minor> releases its line and nothing else releases,
// and conventional commits, where the heaviest commit moves a version.
func releaseFixture() conformance.Fixture {
	on := func(branch string) *pluginv1.BranchRequest {
		return &pluginv1.BranchRequest{Strategy: "release-flow", Branch: branch, DefaultBranch: "main"}
	}
	commits := func(messages ...string) []*pluginv1.ReleaseCommit {
		var out []*pluginv1.ReleaseCommit
		for i, message := range messages {
			out = append(out, &pluginv1.ReleaseCommit{Id: string(rune('a' + i)), Message: message})
		}
		return out
	}
	after := func(last string, messages ...string) *pluginv1.VersionRequest {
		return &pluginv1.VersionRequest{Strategy: "conventional-commits", LastVersion: last, Initial: "0.1.0", Commits: commits(messages...)}
	}
	patch, minor, major, none := int32(typev1.Bump_BUMP_PATCH), int32(typev1.Bump_BUMP_MINOR), int32(typev1.Bump_BUMP_MAJOR), typev1.Bump_BUMP_NONE
	reason := func(commit string, bump int32, summary string) *pluginv1.VersionReason {
		return &pluginv1.VersionReason{Commit: commit, Bump: typev1.Bump(bump), Summary: summary}
	}
	return conformance.Fixture{
		Name: "release",
		Description: &pluginv1.DescribeResponse{Name: "release", Abi: 19, Release: &pluginv1.ReleaseRoles{
			Branchings: []string{"release-flow"}, Versionings: []string{"conventional-commits"},
		}},
		Branches: []conformance.BranchCase{
			{Request: on("release/0.77"), Want: &pluginv1.BranchResponse{Releases: true, Line: "0.77", Channel: "0.77"}},
			{Request: on("release/1.26"), Want: &pluginv1.BranchResponse{Releases: true, Line: "1.26", Channel: "1.26"}},
			{Request: on("main"), Want: &pluginv1.BranchResponse{Reason: "main is the default branch, which releases nothing under Release Flow; a release is made from a branch release/<major>.<minor>"}},
			{Request: on("feat/greet"), Want: &pluginv1.BranchResponse{Reason: `"feat/greet" is no release branch; under Release Flow a release is made from a branch release/<major>.<minor>`}},
			{Request: on("release/1.26.0"), Want: &pluginv1.BranchResponse{Reason: `"release/1.26.0" is no release branch; under Release Flow a release is made from a branch release/<major>.<minor>`}},
			{Request: on("release/01.2"), Want: &pluginv1.BranchResponse{Reason: `"release/01.2" is no release branch; under Release Flow a release is made from a branch release/<major>.<minor>`}},
			{Request: &pluginv1.BranchRequest{Strategy: "github-flow", Branch: "main", DefaultBranch: "main"}, Refused: "UNKNOWN_STRATEGY"},
		},
		Versions: []conformance.VersionCase{
			{Request: after("0.76.1", "fix: trim the greeting"), Want: &pluginv1.VersionResponse{
				Version: "0.76.2", Bump: typev1.Bump(patch), Reasons: []*pluginv1.VersionReason{reason("a", patch, "fix: trim the greeting")},
				Notes: "### Fixes\n\n- trim the greeting",
			}},
			{Request: after("0.76.1", "docs: say more", "fix: trim the greeting\n\nIt was long.", "feat: greet by name"), Want: &pluginv1.VersionResponse{
				Version: "0.77.0", Bump: typev1.Bump(minor), Reasons: []*pluginv1.VersionReason{reason("c", minor, "feat: greet by name"), reason("b", patch, "fix: trim the greeting")},
				Notes: "### Features\n\n- greet by name\n\n### Fixes\n\n- trim the greeting",
			}},
			// Below 1.0 a breaking change is a minor; from 1.0 on, a major.
			{Request: after("0.76.1", "feat!: drop the old greeting"), Want: &pluginv1.VersionResponse{
				Version: "0.77.0", Bump: typev1.Bump(minor), Reasons: []*pluginv1.VersionReason{reason("a", minor, "feat!: drop the old greeting")},
				Notes: "### Breaking changes\n\n- drop the old greeting",
			}},
			{Request: after("1.2.3", "feat!: drop the old greeting"), Want: &pluginv1.VersionResponse{
				Version: "2.0.0", Bump: typev1.Bump(major), Reasons: []*pluginv1.VersionReason{reason("a", major, "feat!: drop the old greeting")},
				Notes: "### Breaking changes\n\n- drop the old greeting",
			}},
			{Request: after("1.2.3", "docs: say more", "not a conventional commit"), Want: &pluginv1.VersionResponse{Bump: none}},
			{Request: after("1.2.3"), Want: &pluginv1.VersionResponse{Bump: none}},
			{Request: after("", "docs: say more"), Want: &pluginv1.VersionResponse{Version: "0.1.0", Bump: none}},
			{Request: after("1.2", "fix: trim the greeting"), Refused: "INVALID_VERSION"},
			{Request: &pluginv1.VersionRequest{Strategy: "manual", LastVersion: "1.2.3", Initial: "0.1.0"}, Refused: "UNKNOWN_STRATEGY"},
		},
	}
}
