package sandbox

import (
	"context"
	"errors"
	"io"
	"testing"
)

// A port that is not yet listening closes the connection unanswered, and a
// caller may go away: each is answered 502 and is no news. A sandbox that
// answers with something that is not HTTP is news.
func TestAPortNotYetListeningIsNoNews(t *testing.T) {
	for _, quiet := range []error{io.EOF, io.ErrUnexpectedEOF, context.Canceled, context.DeadlineExceeded, errors.Join(errors.New("dial"), io.EOF)} {
		if !noNews(quiet) {
			t.Errorf("%v is news", quiet)
		}
	}
	for _, loud := range []error{errors.New("malformed HTTP response"), errors.New("memory: sbx does not open the port 7600"), nil} {
		if noNews(loud) {
			t.Errorf("%v is no news", loud)
		}
	}
}
