// Package schedule decides which action runs next. It packs a test
// action's cases into batches by how long each took before, and runs what
// failed last time first.
package schedule
