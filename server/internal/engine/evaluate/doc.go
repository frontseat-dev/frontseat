// Package evaluate is one evaluation: for one source, from its files to a
// plan. The source of a run is a snapshot: the repositories as they were
// when the run began, which everything the run does reads. What a plugin
// answered is kept by the daemon, in Answers, and given again for as
// long as what the plugin read is the same. A daemon's Session keeps an
// evaluation too, and the plan last asked of it, and holds them to the
// source by what was read: a build or a graph request over source that
// reads the same is given its own copy of what was evaluated, and what
// has changed is evaluated again. A run and a release plan evaluate
// by themselves, over the same Answers. Evaluation folds what projects
// require and what actions read of each other into entity dependencies.
package evaluate
