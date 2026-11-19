package page

import (
	"errors"
	"slices"
	"testing"
)

func TestEveryItemIsListedOnceAcrossThePages(t *testing.T) {
	items := make([]int, 250)
	for i := range items {
		items[i] = i
	}
	var seen []int
	token, pages := "", 0
	for {
		got, next, err := Of(items, 0, token)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		seen = append(seen, got...)
		if next == "" {
			break
		}
		token = next
	}
	if pages != 3 || !slices.Equal(seen, items) {
		t.Fatalf("listed %d items over %d pages; want all 250 in order over 3", len(seen), pages)
	}
}

func TestAPageIsAsLargeAsAskedUpToTheMost(t *testing.T) {
	items := make([]int, Most+5)
	for _, c := range []struct {
		size int32
		want int
		more bool
	}{{0, Default, true}, {7, 7, true}, {Most + 5, Most, true}, {-1, Default, true}} {
		got, next, err := Of(items, c.size, "")
		if err != nil || len(got) != c.want || (next != "") != c.more {
			t.Errorf("size %d: %d items, next %q, %v; want %d", c.size, len(got), next, err, c.want)
		}
	}
	if got, next, err := Of(items[:3], 10, ""); err != nil || len(got) != 3 || next != "" {
		t.Errorf("a list shorter than a page: %d items, next %q, %v; want all and no next", len(got), next, err)
	}
}

func TestATokenNoListGaveIsRefused(t *testing.T) {
	_, given, _ := Of(make([]int, 10), 4, "")
	if _, _, err := Of(make([]int, 10), 4, given); err != nil {
		t.Fatalf("the token a list gave was refused: %v", err)
	}
	for _, token := range []string{"4", "not base64!", "cGFnZTo", "cGFnZTowNA", "cGFnZTotMQ", "eDo0"} {
		if _, _, err := Of(make([]int, 10), 4, token); !errors.Is(err, ErrToken) {
			t.Errorf("token %q: %v; want ErrToken", token, err)
		}
	}
}

func TestATokenPastTheEndIsAnEmptyLastPage(t *testing.T) {
	_, given, _ := Of(make([]int, 10), 8, "")
	got, next, err := Of(make([]int, 5), 8, given)
	if err != nil || len(got) != 0 || next != "" {
		t.Fatalf("a list that shrank: %d items, next %q, %v; want none and no next", len(got), next, err)
	}
}
