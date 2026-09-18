// Package fshidden answers the one question every "hidden files" switch
// asks: does this directory entry count as hidden?
//
// The platform decides. Unix hides a leading dot. Windows hides what
// Explorer hides — FILE_ATTRIBUTE_HIDDEN or FILE_ATTRIBUTE_SYSTEM — and
// keeps the dot rule as well, because the dot-directories developer
// tooling creates (.venv, .next, .cache) carry no attribute there and
// would otherwise be listed and walked by default.
//
// Call sites keep their own policy (a listing may always want .git
// skipped, say); this package only classifies one entry.
package fshidden
