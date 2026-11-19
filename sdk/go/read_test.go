package frontseat_test

import (
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/sdk/go"
)

// sent hands each of its messages, then its end.
type sent struct {
	messages []string
	end      error
}

func (s *sent) Receive() (string, error) {
	if len(s.messages) == 0 {
		return "", s.end
	}
	message := s.messages[0]
	s.messages = s.messages[1:]
	return message, nil
}

func TestEveryMessageIsReceivedInTheOrderItCame(t *testing.T) {
	all, err := frontseat.Received[string](&sent{messages: []string{"a", "b"}, end: io.EOF})
	if err != nil || !slices.Equal(all, []string{"a", "b"}) {
		t.Fatalf("received %q, %v; want a, b", all, err)
	}
}

func TestWhatCameBeforeABreakIsKeptWithTheError(t *testing.T) {
	broken := errors.New("broken")
	all, err := frontseat.Received[string](&sent{messages: []string{"a"}, end: broken})
	if !errors.Is(err, broken) || !slices.Equal(all, []string{"a"}) {
		t.Fatalf("received %q, %v; want a and the break", all, err)
	}
}

func TestEveryPageIsGatheredInOrder(t *testing.T) {
	pages := map[string]struct {
		items []int
		next  string
	}{"": {[]int{1, 2}, "b"}, "b": {[]int{3}, "c"}, "c": {[]int{4}, ""}}
	var asked []string
	got, err := frontseat.Listed(func(token string) ([]int, string, error) {
		asked = append(asked, token)
		return pages[token].items, pages[token].next, nil
	})
	if err != nil || !slices.Equal(got, []int{1, 2, 3, 4}) || !slices.Equal(asked, []string{"", "b", "c"}) {
		t.Fatalf("gathered %v asking %q (%v); want 1 to 4 over three pages", got, asked, err)
	}
}

func TestAPageThatFailsFailsTheWhole(t *testing.T) {
	broken := errors.New("broken")
	got, err := frontseat.Listed(func(token string) ([]int, string, error) {
		if token == "" {
			return []int{1}, "b", nil
		}
		return nil, "", broken
	})
	if !errors.Is(err, broken) || got != nil {
		t.Fatalf("gathered %v, %v; want nothing and the break", got, err)
	}
}

func TestAServerThatAnswersATokenAskedBeforeIsNotAskedForEver(t *testing.T) {
	for name, next := range map[string]map[string]string{"its own": {"": "a", "a": "a"}, "an earlier one": {"": "a", "a": "b", "b": "a"}, "the first": {"": "a", "a": ""}} {
		_, err := frontseat.Listed(func(token string) ([]int, string, error) { return []int{1}, next[token], nil })
		if (err == nil) != (name == "the first") {
			t.Errorf("a page answering %s token: %v", name, err)
		}
	}
}
