package plan

import (
	"fmt"
	"strings"

	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// Check holds a workspace's actions to what a graph is: everything an
// action reads something of is an action of the workspace, and no action
// reads what it leaves itself, through others or not. What actions read
// of each other is known whole before anything runs, so either is a
// model.Mistake that stops a build before it starts, and names the
// catalog file where one wrote the reference.
func Check(actions []Action) error {
	byLabel := map[string]Action{}
	for _, action := range actions {
		byLabel[action.Label.String()] = action
	}
	for _, action := range actions {
		for _, need := range action.Needs {
			if _, held := byLabel[need.String()]; !held {
				repository, file := written(action)
				return &model.Mistake{Repository: repository, File: file, Message: fmt.Sprintf("%s reads what %s leaves, which is not in the workspace", action.Label, need)}
			}
		}
	}
	// A walk along what each action needs. An action met again while it
	// is still being walked is needed by something it needs.
	const walking, walked = 1, 2
	state := map[string]int{}
	var along []string
	var walk func(label string) error
	walk = func(label string) error {
		switch state[label] {
		case walked:
			return nil
		case walking:
			from := 0
			for i, each := range along {
				if each == label {
					from = i
				}
			}
			circle := append(append([]string{}, along[from:]...), label)
			// The file is the first a reference of the circle was
			// written in, whichever action the walk began at.
			repository, file := "", ""
			for _, each := range circle {
				if repository, file = written(byLabel[each]); file != "" {
					break
				}
			}
			return &model.Mistake{Repository: repository, File: file, Message: circle[0] + " reads what " + strings.Join(circle[1:], " leaves, which reads what ") + " leaves"}
		}
		state[label] = walking
		along = append(along, label)
		for _, need := range byLabel[label].Needs {
			if err := walk(need.String()); err != nil {
				return err
			}
		}
		along = along[:len(along)-1]
		state[label] = walked
		return nil
	}
	for _, action := range actions {
		if err := walk(action.Label.String()); err != nil {
			return err
		}
	}
	return nil
}

// written is the catalog file an action's references were written in,
// and its repository; empty for a plugin's action.
func written(action Action) (repository, file string) {
	for _, ref := range action.References {
		if ref.File != "" {
			return ref.Repository, ref.File
		}
	}
	return "", ""
}

// With returns the chosen actions and everything they need, each after
// all it needs. The actions are a workspace's, and have passed Check.
func With(actions, chosen []Action) []Action {
	byLabel := map[string]Action{}
	for _, action := range actions {
		byLabel[action.Label.String()] = action
	}
	seen := map[string]bool{}
	var out []Action
	var add func(action Action)
	add = func(action Action) {
		if seen[action.Label.String()] {
			return
		}
		seen[action.Label.String()] = true
		for _, need := range action.Needs {
			add(byLabel[need.String()])
		}
		out = append(out, action)
	}
	for _, action := range chosen {
		add(action)
	}
	return out
}
