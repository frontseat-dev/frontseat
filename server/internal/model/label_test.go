package model

import "testing"

func TestALabelReadsBackAsItIsWritten(t *testing.T) {
	for _, s := range []string{"//payments:go.compile", "//:mise.lock", "//frontseat/core/plan:go.test-internal-ledger"} {
		l, err := ParseLabel(s)
		if err != nil {
			t.Errorf("ParseLabel(%q): %v", s, err)
			continue
		}
		if got := l.String(); got != s {
			t.Errorf("ParseLabel(%q).String() = %q", s, got)
		}
	}
}

func TestALabelWithoutAPluginIsRefused(t *testing.T) {
	for _, s := range []string{"payments:go.compile", "//payments", "//payments:compile", "//payments:.compile"} {
		if _, err := ParseLabel(s); err == nil {
			t.Errorf("ParseLabel(%q) succeeded, want an error", s)
		}
	}
}
