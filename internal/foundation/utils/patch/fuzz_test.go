package patch

import "testing"

func FuzzParsePatch(f *testing.F) {
	for _, seed := range []string{
		"*** Begin Patch\n*** Add File: a.txt\n+x\n*** End Patch\n",
		"*** Begin Patch\n*** Update File: a.txt\n@@\n-x\n+y\n*** End Patch\n",
		"garbage",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		ops, err := Parse(input)
		if err != nil {
			return
		}
		// A parseable patch must also render without panicking.
		_, _ = Diff(input, nil)
		for _, op := range ops {
			_ = op
		}
	})
}
