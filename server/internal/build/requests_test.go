package build

import (
	"context"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
)

// A run asked for again with the request id of a call whose answer was
// lost is the run the first call made, and starts none; another id, or
// none, starts another.
func TestARunAskedAgainWithItsRequestIDIsTheRunTheFirstMade(t *testing.T) {
	c := serve(t, printing(), oneModule(t), true)
	ask := func(id string) string {
		t.Helper()
		created, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{RequestId: id})
		if err != nil {
			t.Fatal(err)
		}
		wait(t, c.runs, created.GetBuild().GetId())
		return created.GetBuild().GetId()
	}
	first := ask("retry-1")
	if again := ask("retry-1"); again != first {
		t.Errorf("asked again with its request id, the run is %s; want %s", again, first)
	}
	if other := ask("retry-2"); other == first {
		t.Errorf("another request id was answered with the first run %s", first)
	}
	if none, more := ask(""), ask(""); none == more || none == first {
		t.Errorf("two calls with no request id made %s and %s; want two runs of their own", none, more)
	}
	_, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{RequestId: strings.Repeat("r", refusal.RequestIDMost+1)})
	if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != "REQUEST_ID_INVALID" || domain != refusal.Build {
		t.Errorf("a request id too long: %v (%s, %s); want it refused by name", err, reason, domain)
	}
}

// A daemon's runs are listed a page at a time, newest first, and every
// run is on one page; a token no list gave, and a run no one made, are
// refused by name.
func TestRunsAreListedAPageAtATime(t *testing.T) {
	c := serve(t, printing(), oneModule(t), true)
	var made []string
	for range 3 {
		made = append(made, wait(t, c.runs, create(t, c.runs).GetId()).GetId())
	}
	first, err := c.runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	rest, err := c.runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{PageSize: 2, PageToken: first.GetNextPageToken()})
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, r := range append(first.GetBuilds(), rest.GetBuilds()...) {
		listed = append(listed, r.GetId())
	}
	if len(first.GetBuilds()) != 2 || first.GetNextPageToken() == "" || rest.GetNextPageToken() != "" || strings.Join(listed, " ") != strings.Join([]string{made[2], made[1], made[0]}, " ") {
		t.Errorf("listed %v over pages of two (next %q, then %q); want %v newest first", listed, first.GetNextPageToken(), rest.GetNextPageToken(), made)
	}
	_, err = c.runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{PageToken: "made-up"})
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != refusal.PageTokenInvalid {
		t.Errorf("a token no list gave: %v (%s); want it refused by name", err, reason)
	}
	_, err = c.runs.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: "build_v4lue"})
	if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeNotFound || reason != "BUILD_NOT_FOUND" || domain != refusal.Build || strings.Contains(err.Error(), "v4lue") {
		t.Errorf("a run no one made: %v (%s, %s); want it refused by name, without the id asked for", err, reason, domain)
	}
	// The control: a run that was made is found, and so is its action.
	if _, err := logsOf(c.runs, made[0], "//hello:go.lint"); err != nil {
		t.Fatalf("what a run made printed: %v", err)
	}
	_, err = logsOf(c.runs, made[0], "//v4lue:compile")
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeNotFound || reason != "ACTION_NOT_FOUND" || strings.Contains(err.Error(), "v4lue") {
		t.Errorf("an action the run has not: %v (%s); want it refused by name, without the label asked for", err, reason)
	}
	_, err = c.programs.DescribeRun(context.Background(), &buildv1.DescribeRunRequest{RunId: "run_v4lue"})
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeNotFound || reason != "RUN_NOT_FOUND" || strings.Contains(err.Error(), "v4lue") {
		t.Errorf("a program run no one made: %v (%s); want it refused by name, without the id asked for", err, reason)
	}
	_, err = c.releases.DescribeRelease(context.Background(), &buildv1.DescribeReleaseRequest{ReleaseId: "release_v4lue"})
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeNotFound || reason != "RELEASE_NOT_FOUND" || strings.Contains(err.Error(), "v4lue") {
		t.Errorf("a release no one made: %v (%s); want it refused by name, without the id asked for", err, reason)
	}
}
