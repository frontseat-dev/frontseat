package ledger

import "testing"

func TestBalance(t *testing.T) {
	if Balance() != 1 {
		t.Fatal(Balance())
	}
}
