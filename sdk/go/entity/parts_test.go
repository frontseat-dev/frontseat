package entity

import (
	"strings"
	"testing"
)

// Each part of an identity is ASCII, of at most 255, beginning and ending
// with a letter or a digit; anything else is refused, in any part.
func TestAnIdentitysPartsAreASCIIOfAtMost255(t *testing.T) {
	for part := 0; part < 3; part++ {
		for _, v := range []string{"a", "A", "1", "a-b_c.d", strings.Repeat("a", 63), strings.Repeat("a", 64), strings.Repeat("a", 254), strings.Repeat("a", 255)} {
			values := []string{"service-blueprint", "tenant.scope", "name"}
			values[part] = v
			ref, err := Parse(values[0] + ":" + values[1] + "/" + values[2])
			if err != nil {
				t.Errorf("allowed part %d %q: %v", part, v, err)
			} else if err := ref.Validate(); err != nil {
				t.Error(err)
			}
		}
		for _, v := range []string{"", "-a", "a-", "_a", "a_", ".a", "a.", "a b", "a/b", "a:b", "a\x00b", "a\nb", "é", "K", "İ", "aKb", strings.Repeat("a", 256), strings.Repeat("K", 100)} {
			values := []string{"component", "default", "name"}
			values[part] = v
			written := values[0] + ":" + values[1] + "/" + values[2]
			if got, err := Parse(written); err == nil {
				t.Errorf("invalid part %d %q accepted as %q", part, v, got.String())
			}
		}
	}
}
