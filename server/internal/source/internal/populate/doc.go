// Package populate fills a workspace with the repositories it is told
// to hold: it clones each, whole or as a partial checkout of the
// directories asked for, and widens a partial checkout it holds already.
// It resolves no entity: the control plane tells it where each is.
package populate
