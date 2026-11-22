package frontseat

import (
	"errors"
	"slices"
	"testing"
)

// A list ends with the error of a page that fails, after the items that
// came before it, and with an error where a server answers a token it
// was asked with before, which would be asked for again and again. The
// control is a list whose last page answers no token.
func TestAListEndsWhereAPageFailsOrComesRound(t *testing.T) {
	broken := errors.New("broken")
	answers := map[string]func(string) ([]int, string, error){
		"fails": func(token string) ([]int, string, error) {
			return []int{1}, "next", map[string]error{"next": broken}[token]
		},
		"comes round": func(token string) ([]int, string, error) {
			return []int{1}, map[string]string{"": "a", "a": "b", "b": "a"}[token], nil
		},
		"ends": func(token string) ([]int, string, error) { return []int{1}, map[string]string{"": "a"}[token], nil },
	}
	for name, answer := range answers {
		got, err := Collect(paged(answer))
		switch {
		case name == "fails" && (!errors.Is(err, broken) || !slices.Equal(got, []int{1})):
			t.Errorf("a list whose second page fails: %v, %v", got, err)
		case name == "comes round" && err == nil:
			t.Errorf("a list that comes round to a token asked before ended with %v", got)
		case name == "ends" && (err != nil || len(got) != 2):
			t.Errorf("the control: a list of two pages: %v, %v", got, err)
		}
	}
}
