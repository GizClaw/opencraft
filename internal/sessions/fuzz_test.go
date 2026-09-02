package sessions

import "testing"

func FuzzRequireID(f *testing.F) {
	for _, seed := range []string{
		"s-abc",
		"s-../../etc/passwd",
		"",
		"x",
		"../escape",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		_ = ValidID(id)
		_ = requireID(id)
	})
}
