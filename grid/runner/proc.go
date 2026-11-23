package runner

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// descendants is every process beneath one, by the parents the table of
// processes at proc says.
func descendants(proc string, of int) []int {
	stats, _ := filepath.Glob(filepath.Join(proc, "[0-9]*", "stat"))
	children := map[int][]int{}
	for _, at := range stats {
		pid, err := strconv.Atoi(filepath.Base(filepath.Dir(at)))
		if err != nil {
			continue
		}
		if _, _, parent, err := stat(proc, pid); err == nil {
			children[parent] = append(children[parent], pid)
		}
	}
	var out []int
	for next := children[of]; len(next) > 0; {
		pid := next[0]
		next = append(next[1:], children[pid]...)
		out = append(out, pid)
	}
	return out
}

// stat is a process's name, its state and its parent, as the table of
// processes at proc says: pid (comm) state ppid ..., the name anything
// but the last parenthesis.
func stat(proc string, pid int) (string, byte, int, error) {
	line, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, 0, err
	}
	open, shut := bytes.IndexByte(line, '('), bytes.LastIndexByte(line, ')')
	if open < 0 || shut < open {
		return "", 0, 0, errors.New("no process's stat")
	}
	fields := bytes.Fields(line[shut+1:])
	if len(fields) < 2 || len(fields[0]) != 1 {
		return "", 0, 0, errors.New("no process's stat")
	}
	parent, err := strconv.Atoi(string(fields[1]))
	return string(line[open+1 : shut]), fields[0][0], parent, err
}

// living says whether any thread of a process is alive, as the table of
// processes at proc says: its first thread may have ended (a zombie)
// while others run on, and start more.
func living(proc string, pid int) bool {
	tasks, err := os.ReadDir(filepath.Join(proc, strconv.Itoa(pid), "task"))
	if err != nil {
		return false
	}
	for _, task := range tasks {
		line, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "task", task.Name(), "stat"))
		if err != nil {
			continue
		}
		if shut := bytes.LastIndexByte(line, ')'); shut >= 0 && len(line) > shut+2 && line[shut+2] != 'Z' && line[shut+2] != 'X' {
			return true
		}
	}
	return false
}
