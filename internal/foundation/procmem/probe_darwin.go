//go:build darwin && cgo

package procmem

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/resource.h>
#include <dlfcn.h>
#include <string.h>

// The ownership SPI below is what Activity Monitor groups by. WebKit runs
// its renderers as XPC services that launchd reparents, so their ppid is 1
// and the pid tree alone cannot tell they belong to this app. The symbol
// lives in libsystem_kernel but is absent from the public headers, so it is
// resolved at runtime: when it is missing (a future OS dropping it, or a
// system where the symbol is not exported) the snapshot degrades to the pid
// tree, which still covers everything the app spawns itself.
typedef pid_t (*oc_responsible_fn)(pid_t);

static oc_responsible_fn oc_responsible(void) {
	static oc_responsible_fn fn;
	static int resolved;
	if (!resolved) {
		fn = (oc_responsible_fn)dlsym(RTLD_DEFAULT, "responsibility_get_pid_responsible_for_pid");
		resolved = 1;
	}
	return fn;
}

static int oc_has_ownership(void) {
	return oc_responsible() != NULL;
}

static pid_t oc_owner(int pid) {
	oc_responsible_fn fn = oc_responsible();
	return fn == NULL ? 0 : fn((pid_t)pid);
}

// oc_list_pids fills buf with every pid on the system and returns how many
// entries were written (0 when the call failed or the buffer was too
// small).
static int oc_list_pids(int *buf, int max) {
	int bytes = proc_listpids(PROC_ALL_PIDS, 0, buf, max * (int)sizeof(int));
	if (bytes <= 0) {
		return 0;
	}
	return bytes / (int)sizeof(int);
}

// oc_pid_info reads the parent pid and the process name. The name comes
// from pbi_name, which holds up to 2 * MAXCOMLEN bytes: pbi_comm alone
// would cut the platform helper names this package classifies on
// (com.apple.WebKit.WebContent) at 16 bytes.
static int oc_pid_info(int pid, int *ppid, char *name, int namelen) {
	struct proc_bsdinfo info;
	memset(&info, 0, sizeof(info));
	if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info)) != (int)sizeof(info)) {
		return 0;
	}
	*ppid = (int)info.pbi_ppid;
	if (name != NULL && namelen > 0) {
		// pbi_name is empty when the process never registered one, and the
		// short comm is then all the kernel has.
		const char *best = info.pbi_name[0] != '\0' ? info.pbi_name : info.pbi_comm;
		size_t limit = info.pbi_name[0] != '\0'
			? sizeof(info.pbi_name) : sizeof(info.pbi_comm);
		size_t n = (size_t)(namelen - 1) < limit ? (size_t)(namelen - 1) : limit;
		memcpy(name, best, n);
		name[n] = '\0';
	}
	return 1;
}

static int oc_path(int pid, char *buf, int buflen) {
	int bytes = proc_pidpath(pid, buf, (uint32_t)buflen);
	if (bytes <= 0) {
		buf[0] = '\0';
		return 0;
	}
	buf[buflen - 1] = '\0';
	return 1;
}

// oc_usage reads the physical footprint (what Activity Monitor shows) and
// the resident size. Reading another process's usage needs no privileges
// as long as it runs as the same user, which every family member does.
static int oc_usage(int pid, unsigned long long *footprint, unsigned long long *resident) {
	struct rusage_info_v4 usage;
	memset(&usage, 0, sizeof(usage));
	if (proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&usage) != 0) {
		return 0;
	}
	*footprint = usage.ri_phys_footprint;
	*resident = usage.ri_resident_size;
	return 1;
}
*/
import "C"

import (
	"os"
	"unsafe"
)

// maxPIDs bounds the system-wide process list. macOS reports a few hundred
// to a few thousand; a longer list would only mean a machine this sampler
// does not need to describe.
const maxPIDs = 16384

// pathBufLen is PROC_PIDPATHINFO_MAXSIZE (4 * MAXPATHLEN).
const pathBufLen = 4096

// Snapshot reads the family of the current process.
func Snapshot() (Family, error) {
	root := os.Getpid()
	records := readRecords()
	var owners map[int]int
	if hasOwnership() {
		owners = make(map[int]int, len(records))
		for _, rec := range records {
			if owner := ownerOf(rec.pid); owner > 0 {
				owners[rec.pid] = owner
			}
		}
	}
	family := assemble(root, records, owners)
	// Only the members pay for a usage and a path read.
	for i := range family.Members {
		member := &family.Members[i]
		if footprint, resident, ok := usage(member.PID); ok {
			member.Footprint, member.Resident = footprint, resident
		}
		member.Path = executablePath(member.PID)
	}
	return family, nil
}

// readRecords lists every process with its parent and its name.
func readRecords() []record {
	pids := make([]C.int, maxPIDs)
	count := int(C.oc_list_pids(&pids[0], C.int(len(pids))))
	records := make([]record, 0, count)
	for _, pid := range pids[:count] {
		if pid <= 0 {
			continue
		}
		ppid, name, ok := shortInfo(int(pid))
		if !ok {
			continue
		}
		records = append(records, record{pid: int(pid), ppid: ppid, name: name})
	}
	return records
}

func shortInfo(pid int) (int, string, bool) {
	var cppid C.int
	name := make([]byte, 64)
	if C.oc_pid_info(C.int(pid), &cppid, (*C.char)(unsafe.Pointer(&name[0])), C.int(len(name))) == 0 {
		return 0, "", false
	}
	return int(cppid), C.GoString((*C.char)(unsafe.Pointer(&name[0]))), true
}

// hasOwnership reports whether the platform exposes the process-ownership
// API this OS version was built with.
func hasOwnership() bool {
	return C.oc_has_ownership() != 0
}

// ownerOf returns the pid this process is reported to belong to, or 0 when
// the platform has no ownership API.
func ownerOf(pid int) int {
	return int(C.oc_owner(C.int(pid)))
}

func executablePath(pid int) string {
	path := make([]byte, pathBufLen)
	if C.oc_path(C.int(pid), (*C.char)(unsafe.Pointer(&path[0])), C.int(len(path))) == 0 {
		return ""
	}
	return C.GoString((*C.char)(unsafe.Pointer(&path[0])))
}

func usage(pid int) (uint64, uint64, bool) {
	var footprint, resident C.ulonglong
	if C.oc_usage(C.int(pid), &footprint, &resident) == 0 {
		return 0, 0, false
	}
	return uint64(footprint), uint64(resident), true
}
