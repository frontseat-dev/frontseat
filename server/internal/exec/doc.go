// Package exec runs a plan on a grid: it stages each action's inputs,
// asks the grid, and reports what happened as events.
//
//	reapi       the client of the Remote Execution API
//	stage       an action's input root, from source and other actions' outputs
//	schedule    which action runs next, and how tests are batched
//	tools       the tools actions run with, made known to the grid
//	testreport  JUnit XML in, test cases and their timings out
package exec
