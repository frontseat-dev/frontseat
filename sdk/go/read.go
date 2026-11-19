package frontseat

import (
	"errors"
	"io"
	"iter"
)

// Receiver is a stream of a call that hands one message at a time, and
// io.EOF where it has ended, as a Connect server stream does to its
// client.
type Receiver[T any] interface {
	Receive() (T, error)
}

// Received is every message of a stream, in the order they came, read
// to its end. Where the stream breaks, it is those that came before,
// with the error.
func Received[T any](stream Receiver[T]) ([]T, error) {
	var messages []T
	for message, err := range each(stream) {
		if err != nil {
			return messages, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// each hands a stream's messages one at a time, as they come, to its
// end; where it breaks, the error, last.
func each[T any](stream Receiver[T]) iter.Seq2[T, error] {
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

// Listed gathers every item a List call answers, page after page, in the
// order the call lists them. Page is the call: given the token of the
// page to ask for, empty for the first, it answers that page's items
// and the token of the next, empty after the last.
func Listed[T any](page func(token string) ([]T, string, error)) ([]T, error) {
	var all []T
	token := ""
	asked := map[string]bool{}
	for {
		asked[token] = true
		items, next, err := page(token)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			return all, nil
		}
		if asked[next] {
			return nil, errors.New("the server answered a page with its own token, and would be asked for it again and again")
		}
		token = next
	}
}

// stream is a stream of a call as its client holds it: received, then
// closed.
type stream[T any] interface {
	Receiver[T]
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
