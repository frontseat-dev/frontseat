package publish

import (
	"strings"
	"testing"
)

// An artifact's name is one a publish can be given: a plain file name
// of one part that is no option, no hidden file and not the file of
// sums. The controls are names tools give what they make.
func TestAnArtifactIsNamedAsAPublishCanBeGivenIt(t *testing.T) {
	for name, says := range map[string]string{
		"":              "no plain file name",
		"a/b":           "no plain file name",
		"..":            "no plain file name",
		"a\nb":          "no plain file name",
		"--repo":        "begins as an option or a hidden file does",
		".hidden":       "begins as an option or a hidden file does",
		"checksums.txt": "which is the file of sums",
	} {
		if err := Named(name); err == nil || !strings.Contains(err.Error(), says) || strings.Contains(err.Error(), "\n") {
			t.Errorf("%q: %v, want %q", name, err, says)
		}
	}
	for _, name := range []string{"greet-linux-amd64", "greet-1.4.0.tgz", "frontseat_plugin_go.wasm"} {
		if err := Named(name); err != nil {
			t.Errorf("%q was refused: %v", name, err)
		}
	}
}

// What is said of a publish leaves out what it was given of where the
// daemon runs: a value that begins another, one that ends in a line's
// end and is printed without, and each line of one of several.
func TestWhatAPublishPrintedIsSaidWithoutWhatItWasGiven(t *testing.T) {
	values := []string{"abc123", "abc123-the-rest-of-a-longer-secret", "ends-in-a-newline\n", "first-line\nsecond-line"}
	got := without("a abc123-the-rest-of-a-longer-secret b abc123 c ends-in-a-newline. d second-line e first-line f", values)
	if got != "a … b … c …. d … e … f" {
		t.Errorf("it is said as %q", got)
	}
	if got := without("nothing of it", nil); got != "nothing of it" {
		t.Errorf("with nothing given it is said as %q", got)
	}
}
