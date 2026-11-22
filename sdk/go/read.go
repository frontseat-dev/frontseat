package frontseat

import (
	"errors"
	"io"
	"iter"
)

// receiver is a stream of a call that hands one message at a time, and
// io.EOF where it has ended, as a Connect server stream does to its
// client.
type receiver[T any] interface {
	Receive() (T, error)
}

// each hands a stream's messages one at a time, as they come, to its
// end; where it breaks, the error, last.
func each[T any](stream receiver[T]) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for {
			message, err := stream.Receive()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(message, err)
				return
			}
			if !yield(message, nil) {
				return
			}
		}
	}
}

// Collect gathers what a sequence the SDK hands back yields, in order:
// every item of a list, every line of a log. Where it ends with an
// error, it is the items that came before, with the error.
func Collect[T any](seq iter.Seq2[T, error]) ([]T, error) {
	var all []T
	for item, err := range seq {
		if err != nil {
			return all, err
		}
		all = append(all, item)
	}
	return all, nil
}

// paged hands the items a List call answers one at a time, asking for
// each page as the one before is read through, to the last; where a
// page fails, the error, last. Page is the call: given the token of the
// page to ask for, empty for the first, it answers that page's items and
// the token of the next, empty after the last.
func paged[T any](page func(token string) ([]T, string, error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var none T
		token := ""
		asked := map[string]bool{}
		for {
			asked[token] = true
			items, next, err := page(token)
			if err != nil {
				yield(none, err)
				return
			}
			for _, item := range items {
				if !yield(item, nil) {
					return
				}
			}
			if next == "" {
				return
			}
			if asked[next] {
				yield(none, errors.New("the server answered a page with its own token, and would be asked for it again and again"))
				return
			}
			token = next
		}
	}
}

// stream is a stream of a call as its client holds it: received, then
// closed.
type stream[T any] interface {
	receiver[T]
	Close() error
}

// flattened hands the items of every message of a stream one at a time,
// to the stream's end, and closes it; where opening it or reading it
// fails, the error, last.
func flattened[T, E any](open func() (stream[T], error), items func(T) []E) iter.Seq2[E, error] {
	return func(yield func(E, error) bool) {
		var none E
		opened, err := open()
		if err != nil {
			yield(none, err)
			return
		}
		defer opened.Close()
		for message, err := range each[T](opened) {
			if err != nil {
				yield(none, err)
				return
			}
			for _, item := range items(message) {
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}
