// Package semver parses and orders the version strings this repository
// writes into manifests. It exists once so the plugin registry, the
// application registry and the update checker cannot disagree about
// whether "0.3.0-rc.1" is older than "0.3.0": the same rule answers a
// minHostVersion gate and an update offer.
//
// The shape is semver's, with the parts a local manifest needs: a
// dotted numeric core with an optional "v" prefix, an optional
// "-prerelease" suffix, and "+build" metadata that is parsed away
// (build metadata never affects precedence).
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Bounds keep a hostile or accidental manifest from turning a short
// string into parser work.
const (
	maxLen        = 64
	maxSegments   = 4
	maxPrerelease = 8
	maxIdentifier = 32
)

// parsed is a semver-shaped dotted numeric version with an optional
// prerelease. Build metadata is ignored for ordering.
type parsed struct {
	core []int
	pre  []string
}

// Valid reports whether v is a version string a manifest may carry:
// "1", "1.2", "1.2.3" (an optional "v" prefix), an optional
// "-prerelease" suffix and "+build" metadata that is ignored. Numeric
// segments must be non-negative integers without leading zeros.
func Valid(v string) error {
	_, err := parse(v)
	return err
}

// Compare orders two version strings semver-style: dotted numeric core,
// then prerelease precedence (release > any prerelease, numeric
// identifiers sort before alphanumeric, shorter prerelease sorts first
// when all identifiers are equal). Missing core segments count as zero,
// so "1" and "1.0.0" compare equal. Both versions are validated first,
// so a caller that got a comparison back knows both strings were
// well-formed.
func Compare(a, b string) (int, error) {
	pa, err := parse(a)
	if err != nil {
		return 0, err
	}
	pb, err := parse(b)
	if err != nil {
		return 0, err
	}
	max := len(pa.core)
	if len(pb.core) > max {
		max = len(pb.core)
	}
	for i := 0; i < max; i++ {
		var x, y int
		if i < len(pa.core) {
			x = pa.core[i]
		}
		if i < len(pb.core) {
			y = pb.core[i]
		}
		if x < y {
			return -1, nil
		}
		if x > y {
			return 1, nil
		}
	}
	if len(pa.pre) == 0 && len(pb.pre) == 0 {
		return 0, nil
	}
	if len(pa.pre) == 0 {
		return 1, nil
	}
	if len(pb.pre) == 0 {
		return -1, nil
	}
	for i := 0; i < len(pa.pre) || i < len(pb.pre); i++ {
		if i >= len(pa.pre) {
			return -1, nil
		}
		if i >= len(pb.pre) {
			return 1, nil
		}
		x := pa.pre[i]
		y := pb.pre[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn < yn {
				return -1, nil
			}
			if xn > yn {
				return 1, nil
			}
		case xerr == nil:
			return -1, nil // numeric identifiers sort below alphanumeric
		case yerr == nil:
			return 1, nil
		default:
			if x < y {
				return -1, nil
			}
			if x > y {
				return 1, nil
			}
		}
	}
	return 0, nil
}

// parse accepts "1", "1.2", "1.2.3", optional "v" prefix, an optional
// "-prerelease" suffix (dot-separated identifiers) and "+build"
// metadata.
func parse(v string) (parsed, error) {
	orig := v
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" || len(v) > maxLen {
		return parsed{}, fmt.Errorf("invalid version %q", orig)
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	if v == "" {
		return parsed{}, fmt.Errorf("invalid version %q", orig)
	}
	parts := strings.Split(v, ".")
	if len(parts) > maxSegments {
		return parsed{}, fmt.Errorf("invalid version %q", orig)
	}
	core := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return parsed{}, fmt.Errorf("invalid version %q", orig)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return parsed{}, fmt.Errorf("invalid version %q", orig)
		}
		core = append(core, n)
	}
	var preIDs []string
	if pre != "" {
		ids := strings.Split(pre, ".")
		if len(ids) > maxPrerelease {
			return parsed{}, fmt.Errorf("invalid version %q", orig)
		}
		for _, id := range ids {
			if id == "" || len(id) > maxIdentifier {
				return parsed{}, fmt.Errorf("invalid version %q", orig)
			}
			preIDs = append(preIDs, id)
		}
	}
	return parsed{core: core, pre: preIDs}, nil
}
