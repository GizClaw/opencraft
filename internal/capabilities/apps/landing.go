// Where a package's files would land, and whether the platform would
// take them there.
//
// The preflight and the install both have to answer the same question —
// can these paths exist where the copy would put them — and they have to
// answer it the same way: the check walks what an install would copy and
// measures it against one spelling of the content root, so a package the
// wizard called installable is never refused halfway through the copy.

package apps

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// PathTooLong is one file of a package whose landing path the platform
// would refuse. The preflight reports it as a row and an install refuses
// it as an error, and both carry this sentence, because the fix is the
// packager's either way.
type PathTooLong struct {
	// Entry is where the file sits in the package, slash-spelled like
	// every path a manifest writes.
	Entry string
	// Landing is where it would land: the application's content root
	// plus Entry.
	Landing string
	// Length is Landing in the platform's own units (see pathLength) and
	// Limit the longest path it may be.
	Length int
	Limit  int
}

func (e *PathTooLong) Error() string {
	return fmt.Sprintf(
		"this file would land on a %d-character path, past the %d this "+
			"system's file APIs accept; the package needs shorter "+
			"directory names, or a shorter application home",
		e.Length, e.Limit)
}

// pathLength counts a path the way Windows does: in UTF-16 code units,
// not bytes. A package whose directories are named in Chinese is three
// bytes per character but one code unit, and counting bytes would refuse
// paths Windows takes.
func pathLength(path string) int {
	return len(utf16.Encode([]rune(path)))
}

// firstLongLandingPath walks one package tree — the directory an install
// would copy, or the extraction of the archive it would copy from — and
// returns the first file whose landing path under dest breaks limit, or
// nil. limit <= 0 checks nothing.
//
// The walk mirrors copyTree, so it answers about the files that would
// actually land: a .git directory full of long names is not one of them,
// and neither is a link the copy refuses on its own account. One file is
// enough to report — a package long enough for the platform is long
// throughout — so the first one found stops the walk.
func firstLongLandingPath(tree, dest string, limit int) (*PathTooLong, error) {
	if limit <= 0 {
		return nil, nil
	}
	var found *PathTooLong
	err := filepath.WalkDir(tree,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == tree {
				return nil
			}
			rel, err := filepath.Rel(tree, path)
			if err != nil {
				return err
			}
			if strings.HasPrefix(filepath.Base(rel), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() && !d.Type().IsRegular() {
				return nil
			}
			landing := filepath.Join(dest, rel)
			if n := pathLength(landing); n > limit {
				found = &PathTooLong{
					Entry:   filepath.ToSlash(rel),
					Landing: landing,
					Length:  n,
					Limit:   limit,
				}
				return fs.SkipAll
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// landingRoot is where a candidate package's files would land: the
// content root of the application it declares. It is the same string
// contentDir returns once the install exists, computed without requiring
// one, so the preflight's answer and the install's answer are about the
// same directory.
func (s *Store) landingRoot(id string) string {
	return filepath.Join(s.root, id, "content")
}

// checkLandingPaths refuses a package whose files the platform would not
// take where the install would put them. The installs ask this themselves
// rather than trusting the preflight to have run: without it the copy
// fails partway, on files already written, with an error that says
// nothing about lengths.
func (s *Store) checkLandingPaths(tree, dest string) error {
	tooLong, err := firstLongLandingPath(tree, dest, s.pathLimit)
	if err != nil {
		return fmt.Errorf("apps: scan package paths: %w", err)
	}
	if tooLong != nil {
		return tooLong
	}
	return nil
}
