package record

import "fmt"

// Checkout is a repository to clone into a workspace.
type Checkout struct {
	// Name is the workspace's name for it.
	Name string
	// Remote is its URL.
	Remote string
	// Ref is the branch or tag to stand at; empty for the remote's
	// default branch.
	Ref string
	// Roots are the directories to take of it; empty for all of it.
	Roots []string
}

// Within holds a checkout to the workspace it is cloned into: its name
// is one directory of it, the directories taken are beneath it, and the
// remote is there. A checkout is named by whoever asks, or by a catalog.
func (c Checkout) Within() error {
	if err := Directory(c.Name); err != nil {
		return err
	}
	if c.Remote == "" {
		return fmt.Errorf("%w: the repository %s has no remote to be cloned from", ErrUnfit, c.Name)
	}
	for _, root := range c.Roots {
		if err := Beneath(c.Name, "the directory", root); err != nil {
			return err
		}
	}
	return nil
}
