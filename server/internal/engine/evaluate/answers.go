package evaluate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

// Answers is what a daemon keeps of what its plugins answered, from one
// evaluation to the next. A plugin answers from what it is asked and
// what it reads of the repository, and from nothing else; so an answer
// is given again, and the plugin not asked, for as long as the same
// module is asked the same thing of a repository that holds what the
// plugin read of it. Whatever a plugin read that has changed, and
// whatever its patterns matched that is no longer what they match, has
// it asked again.
//
// An answer nothing has asked for in three hours is let go: by the
// clock, and not by how many evaluations there were, since the graph
// is asked for and one profile is built many times between two builds
// of another.
// A request a session answers from the evaluation it keeps asks no
// plugin, and asks for every answer that evaluation rests on all the
// same (stillAsked), so they are not let go while they are used.
//
// It is the daemon's one record of what plugins answered. The session
// is given it, and so is whatever evaluates beside the session, a run
// and a release plan, and what reports on a check: nothing is asked or
// kept twice. What it keeps is counted under the session's limits;
// where they are gone over, what the session's evaluation does not rest
// on is let go, and what it rests on is not.
type Answers struct {
	// now is the time; time.Now unless a test says otherwise.
	now func() time.Time

	mu   sync.Mutex
	kept map[string]*answer
	// repositories is what each repository said of how it is built, by
	// the workspace's name for it.
	repositories map[string]*saying
}

// saying is what a repository said of how it is built and which tools
// it pins, and what was read to know.
type saying struct {
	said   *config.Repository
	pinned map[string]string
	reads  []pluginhost.Read
	// last is when it was last asked for.
	last time.Time
}

// answer is what a plugin answered a call, and what it read to.
type answer struct {
	response []byte
	reads    []pluginhost.Read
	// last is when it was last asked for.
	last time.Time
}

// unasked is how long an answer is kept without being asked for.
const unasked = 3 * time.Hour

// clock returns the time by the clock on the wall.
func (a *Answers) clock() time.Time {
	if a.now != nil {
		return a.now().Round(0)
	}
	return time.Now().Round(0)
}

// begin starts an evaluation, and lets go of the answers that have not
// been asked for in a while.
func (a *Answers) begin() {
	now := a.clock()
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, each := range a.kept {
		if now.Sub(each.last) > unasked {
			delete(a.kept, key)
		}
	}
	for name, each := range a.repositories {
		if now.Sub(each.last) > unasked {
			delete(a.repositories, name)
		}
	}
}

// said returns what a repository says of how it is built, and the tools
// it pins. It is read from the repository's files, every catalog file
// among them, and kept with what was read: it is read again only where
// a file that said it has changed, gone or come, as a plugin is asked
// again only then. What a repository says wrongly is not kept: it is
// read, and refused, each time.
func (a *Answers) said(ctx context.Context, view repositoryView) (*config.Repository, map[string]string, error) {
	a.mu.Lock()
	before := a.repositories[view.repository]
	a.mu.Unlock()
	if before != nil {
		same, err := pluginhost.Same(ctx, view, before.reads)
		if err != nil {
			return nil, nil, err
		}
		if same {
			a.mu.Lock()
			before.last = a.clock()
			a.mu.Unlock()
			return before.said, before.pinned, nil
		}
	}
	through := &pluginhost.Recorder{Source: view}
	said, err := config.Read(ctx, view.repository, through)
	if err != nil {
		return nil, nil, err
	}
	pinned, err := tools(ctx, view.repository, through)
	if err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.repositories == nil {
		a.repositories = map[string]*saying{}
	}
	a.repositories[view.repository] = &saying{said: said, pinned: pinned, reads: through.Reads(), last: a.clock()}
	return said, pinned, nil
}

// ask has a plugin answer a call, or gives what it answered the same
// call before where the repository holds what it read then. It reports
// whether the answer was given again.
func (a *Answers) ask(ctx context.Context, plugin *pluginhost.Plugin, view repositoryView, method string, request, response proto.Message) (again bool, err error) {
	again, _, err = a.askObserved(ctx, plugin, view, method, request, response)
	return again, err
}

// askObserved also returns the tracked observations, including on replay, so
// generation discovery can prove it did not depend on generated content.
func (a *Answers) askObserved(ctx context.Context, plugin *pluginhost.Plugin, view repositoryView, method string, request, response proto.Message) (again bool, observed []pluginhost.Read, err error) {
	again, _, value, err := a.answerObserved(ctx, plugin, view, method, request, response)
	if err != nil {
		return false, nil, err
	}
	return again, value.reads, nil
}

// answerObserved also returns what was asked, as bytes, and the answer that
// is kept, for a session to hold an evaluation to its source by.
// The request bytes, response bytes and reads are immutable. Only last changes,
// under Answers.mu. Callers still receive their own decoded response to modify.
func (a *Answers) answerObserved(ctx context.Context, plugin *pluginhost.Plugin, view repositoryView, method string, request, response proto.Message) (again bool, asked []byte, value *answer, err error) {
	asked, err = proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return false, nil, nil, err
	}
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\n%s\n%d %s\n", plugin.Digest, method, len(view.repository), view.repository)
	sum.Write(asked)
	key := hex.EncodeToString(sum.Sum(nil))

	a.mu.Lock()
	before := a.kept[key]
	a.mu.Unlock()
	if before != nil {
		same, err := pluginhost.Same(ctx, view, before.reads)
		if err != nil {
			return false, nil, nil, err
		}
		if same {
			if err := proto.Unmarshal(before.response, response); err != nil {
				return false, nil, nil, err
			}
			a.mu.Lock()
			before.last = a.clock()
			a.mu.Unlock()
			return true, asked, before, nil
		}
	}
	reads, err := plugin.Call(ctx, view, method, request, response)
	if err != nil {
		return false, nil, nil, err
	}
	kept, err := a.keep(key, response, reads)
	return false, asked, kept, err
}

// keep keeps what a plugin answered under its key, with what it read
// to answer, and returns what is kept.
func (a *Answers) keep(key string, response proto.Message, reads []pluginhost.Read) (*answer, error) {
	answered, err := proto.Marshal(response)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.kept == nil {
		a.kept = map[string]*answer{}
	}
	a.kept[key] = &answer{response: answered, reads: reads, last: a.clock()}
	return a.kept[key], nil
}

// askAlone has a plugin answer a call that is told nothing of a
// repository, or gives what it answered the same call before. The
// plugin is asked with no source at all, as it is asked what it is: a
// read through the host fails the call, so an answer is one of what it
// was told alone, and is the same for every repository that asks the
// same. It reports whether the answer was given again.
func (a *Answers) askAlone(ctx context.Context, plugin *pluginhost.Plugin, method string, request, response proto.Message) (again bool, err error) {
	again, _, err = a.answerAlone(ctx, plugin, method, request, response)
	return again, err
}

// answerAlone is askAlone, and returns the answer that is kept as well,
// for a session to know what its evaluation rests on.
func (a *Answers) answerAlone(ctx context.Context, plugin *pluginhost.Plugin, method string, request, response proto.Message) (again bool, kept *answer, err error) {
	asked, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return false, nil, err
	}
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\n%s\nalone\n", plugin.Digest, method)
	sum.Write(asked)
	key := hex.EncodeToString(sum.Sum(nil))

	a.mu.Lock()
	before := a.kept[key]
	if before != nil {
		before.last = a.clock()
	}
	a.mu.Unlock()
	if before != nil {
		return true, before, proto.Unmarshal(before.response, response)
	}
	if _, err := plugin.Call(ctx, nil, method, request, response); err != nil {
		return false, nil, err
	}
	kept, err = a.keep(key, response, nil)
	return false, kept, err
}
