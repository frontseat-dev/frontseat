// Package page cuts what a List call answers into pages: page_size and
// page_token in, next_page_token out, as every List of the API takes and
// gives them.
package page

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// The sizes of a page: what a request that asks none is given, and the
// most any is given.
const (
	Default = 100
	Most    = 1000
)

// ErrToken is a page token that no List gave: one a caller made, or one
// cut short.
var ErrToken = errors.New("the page token is not one a list gave")

// prefix marks a token as this package's, so that another string is not
// read as an offset.
const prefix = "page:"

// Of is the page of items a request asks for, and the token of the page
// after it; empty on the last. The items are in the order the call lists
// them, and a token names where the next page begins in that order: a
// list that changed between pages may repeat or skip what moved.
func Of[T any](items []T, size int32, token string) ([]T, string, error) {
	from, err := offset(token)
	if err != nil {
		return nil, "", err
	}
	n := int(size)
	switch {
	case n <= 0:
		n = Default
	case n > Most:
		n = Most
	}
	if from >= len(items) {
		return nil, "", nil
	}
	to := min(from+n, len(items))
	next := ""
	if to < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(prefix + strconv.Itoa(to)))
	}
	return items[from:to], next, nil
}

// offset reads where a token says a page begins: the start, for none.
func offset(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, ErrToken
	}
	digits, ok := strings.CutPrefix(string(raw), prefix)
	if !ok {
		return 0, ErrToken
	}
	at, err := strconv.Atoi(digits)
	if err != nil || at <= 0 || strconv.Itoa(at) != digits {
		return 0, ErrToken
	}
	return at, nil
}

// All is every item a List call answers, asked for page after page, in
// the order the call lists them, for a client of one: ask is the call,
// given the token of the page to ask for, empty for the first, and
// answering that page's items and the token of the next, empty after
// the last. Where a page fails, it is nil, with the error; a server that
// answers a token it was asked with before is not asked again.
func All[T any](ask func(token string) ([]T, string, error)) ([]T, error) {
	var all []T
	seen := map[string]bool{}
	for token := ""; ; {
		seen[token] = true
		items, next, err := ask(token)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		switch {
		case next == "":
			return all, nil
		case seen[next]:
			return nil, ErrLoop
		}
		token = next
	}
}

// ErrLoop is a List that answered a page with a token it was asked with
// before, and would be asked for the same pages for ever.
var ErrLoop = errors.New("the server answered a page with a token it was asked with before")
