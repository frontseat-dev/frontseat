package record

import "testing"

func TestAnEntityLivesInItsRepositoryAtItsDirectory(t *testing.T) {
	for want, located := range map[string]Located{
		"platform/gateway": {Repository: "platform", Root: "gateway"},
		"platform":         {Repository: "platform"},
		"gateway":          {Root: "gateway"},
	} {
		if got := located.Path(); got != want {
			t.Errorf("%+v lives at %q, want %q", located, got, want)
		}
	}
}
