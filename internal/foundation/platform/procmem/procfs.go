// procfs.go reads a /proc-like tree. This is the platform-independent half
// of the Linux probe (probe_linux.go adds nothing but the mount point and
// the ordering), and the file is deliberately untagged so the parser runs
// under the tests on every platform the repo builds on.
package procmem

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readProcFS lists every process under a /proc mount, reading what the
// family snapshot needs before membership is known: the parent pid and the
// executable's name.
func readProcFS(root string) []record {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	records := make([]record, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join(root, entry.Name(), "stat"))
		if err != nil {
			// The process is gone: /proc entries outlive their process
			// for as long as the read takes.
			continue
		}
		ppid, comm, ok := parseProcStat(string(stat))
		if !ok {
			continue
		}
		records = append(records, record{
			pid:  pid,
			ppid: ppid,
			name: procName(root, entry.Name(), comm),
		})
	}
	return records
}

// parseProcStat reads the parent pid and the process name out of one
// /proc/<pid>/stat line. The name is whatever the process called itself —
// it may contain spaces and parentheses — so it is taken between the first
// '(' and the last ')' rather than as the third whitespace-separated
// token, and the parent pid is the field after the closing parenthesis.
func parseProcStat(line string) (ppid int, name string, ok bool) {
	open := strings.IndexByte(line, '(')
	closing := strings.LastIndexByte(line, ')')
	if open < 0 || closing < open {
		return 0, "", false
	}
	name = line[open+1 : closing]
	// state, ppid, pgrp, session, ...
	fields := strings.Fields(line[closing+1:])
	if len(fields) < 2 {
		return 0, "", false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", false
	}
	return ppid, name, true
}

// procName prefers the argv[0] base name over the kernel's comm field:
// comm is truncated to 15 bytes, which cuts exactly the names this package
// classifies on (WebKitNetworkProcess becomes WebKitNetworkProces).
func procName(root, pidDir, comm string) string {
	cmdline, err := os.ReadFile(filepath.Join(root, pidDir, "cmdline"))
	if err != nil {
		return comm
	}
	argv0, _, _ := strings.Cut(string(cmdline), "\x00")
	if base := filepath.Base(argv0); base != "" && base != "." && base != "/" {
		return base
	}
	return comm
}

// execPath reads what a process is running (/proc/<pid>/exe) when the
// process still exists.
func execPath(root, pidDir string) string {
	target, err := os.Readlink(filepath.Join(root, pidDir, "exe"))
	if err != nil {
		return ""
	}
	return target
}

// procMemory reads one process's memory out of /proc. Pss (smaps_rollup)
// is the closest thing Linux has to the physical footprint the macOS probe
// reads: shared pages are divided between the processes that map them
// instead of being counted whole. Kernels without smaps_rollup fall back to
// the resident size, which then counts shared pages in full.
func procMemory(root, pidDir string) (footprint, resident uint64, ok bool) {
	rollup, err := os.ReadFile(filepath.Join(root, pidDir, "smaps_rollup"))
	if err == nil {
		rss, pss, shared := parseSmapsRollup(string(rollup))
		if pss > 0 {
			return pss, rss, true
		}
		if rss > 0 {
			return rss + shared, rss, true
		}
	}
	if resident, ok := parseStatm(filepath.Join(root, pidDir, "statm")); ok {
		return resident, resident, true
	}
	return 0, 0, false
}

// parseSmapsRollup sums the Rss/Pss/Shared_* kilobytes of a smaps_rollup
// body. Shared_Clean and Shared_Dirty are what the rollup reports instead
// of a single shared number.
func parseSmapsRollup(body string) (rss, pss, shared uint64) {
	for _, line := range strings.Split(body, "\n") {
		label, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch label {
		case "Rss":
			rss = kb * 1024
		case "Pss":
			pss = kb * 1024
		case "Shared_Clean", "Shared_Dirty":
			shared += kb * 1024
		}
	}
	return rss, pss, shared
}

// parseStatm reads the resident size out of /proc/<pid>/statm, whose
// fields are counted in pages.
func parseStatm(path string) (resident uint64, ok bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(body))
	// size, resident, shared, text, lib, data, dt
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}
