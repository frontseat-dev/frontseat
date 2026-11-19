package build

// What a workspace's daemon keeps of what it evaluated, and how many graph
// requests it lets hold source at once (owner, 2026-11-15). Each is a
// measured starting point, and changed only with a measurement beside it.
// What is kept is held by TestAThousandModulesAreKeptWithinWhatADaemonKeeps;
// how many are admitted, by TestQueuedGraphRequestsOwnAsManySourcesAsAreAdmitted
// for the session and by the daemon's own tests, which make their sessions
// with these.
const (
	// retainedRecords and retainedWireBytes bound what a session keeps
	// between requests: plugin answers, repository configuration and the
	// queries of its trace, and the bytes of the answers. A request that
	// goes over them still returns its whole result; the session then lets
	// go of the answers its evaluation does not rest on, and, where that is
	// not enough, of the evaluation, never of an answer it rests on, so a
	// workspace that does not fit is evaluated for every request, and no
	// more is asked of plugins for it than by a daemon that keeps no
	// evaluation. A workspace of 1,000 Go modules that are
	// each a Component keeps 6,701 records and 1,612,500 bytes of one profile
	// (TestAThousandModulesAreKeptWithinWhatADaemonKeeps). The records were
	// 8,192 until that was measured: a workspace of some 1,220 such modules
	// would have gone over, so they are 32,768 (owner, 2026-11-15), which
	// holds some 4,900 at that ratio, of one profile; no workspace of that
	// size has been measured. They say when an evaluation is let go, and
	// bound neither the answers a workspace rests on nor the daemon's
	// memory: graphs, source and plugins are not counted.
	retainedRecords   = 32768
	retainedWireBytes = 8 << 20
	// graphRequestsAdmitted graph requests own source at once, each from
	// taking it through the projection it returns; the rest wait, holding
	// none, and are not capped in number: one waiting costs about 1.2 KB.
	// One at a time made a burst of eight requests 33% slower than no
	// bound and a burst of thirty-two 20% slower; two made neither slower
	// (32.9 ms and 124.2 ms, against 33.8 ms and 142.3 ms unbounded), and
	// each one admitted costs one source of the workspace while 64 wait
	// (349 KB, 614 KB and 1.14 MB at one, two and four for 500 modules,
	// against 16.9 MB with no bound); those were measured once, before the
	// work was brought onto ABI 14, and nothing here makes them again. A
	// build takes the snapshot it began with and waits for none of them to
	// take source; the evaluation itself is for one request at a time,
	// builds and graph queries alike.
	graphRequestsAdmitted = 2
)
