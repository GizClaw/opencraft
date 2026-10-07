package maxpath

import (
	"testing"

	goruntime "runtime"
)

// TestLimitAnswersForEveryPlatform runs on any host: the Windows answer
// is the one with a cap in it, and it is MAX_PATH less the NUL.
func TestLimitAnswersForEveryPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos string
		want int
	}{
		{"windows", 259},
		{"darwin", 0},
		{"linux", 0},
		{"", 0},
	} {
		if got := Limit(tc.goos); got != tc.want {
			t.Errorf("Limit(%q) = %d, want %d", tc.goos, got, tc.want)
		}
	}
}

// TestHostLimitIsThisHosts: the process asks about the OS it is running
// on, which is what makes the check live on the Windows lane and cost
// nothing elsewhere.
func TestHostLimitIsThisHosts(t *testing.T) {
	if got, want := HostLimit(), Limit(goruntime.GOOS); got != want {
		t.Errorf("HostLimit() = %d on %s, want %d",
			got, goruntime.GOOS, want)
	}
	if got := HostLimit(); goruntime.GOOS == "windows" && got != 259 {
		t.Errorf("HostLimit() = %d on windows, want 259", got)
	}
}
