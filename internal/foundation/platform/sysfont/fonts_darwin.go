//go:build darwin && cgo

package sysfont

/*
#cgo LDFLAGS: -framework CoreText -framework CoreFoundation

#include <CoreFoundation/CoreFoundation.h>
#include <CoreText/CoreText.h>
#include <stdlib.h>

// The helpers traffic in void *: cgo cannot compare or convert the opaque
// CF types, and none of that casting belongs in Go.

// oc_available_families copies the CoreText family catalogue (the caller owns
// it, hence the oc_release below).
static void *oc_available_families(void) {
	return (void *)CTFontManagerCopyAvailableFontFamilyNames();
}

static long oc_family_count(void *families) {
	if (families == NULL) {
		return 0;
	}
	return (long)CFArrayGetCount((CFArrayRef)families);
}

// oc_family_name returns a malloc'd UTF-8 copy of one family name, or NULL
// when the entry is missing or cannot be encoded.
static char *oc_family_name(void *families, long index) {
	if (families == NULL) {
		return NULL;
	}
	CFStringRef name = (CFStringRef)CFArrayGetValueAtIndex(
		(CFArrayRef)families, (CFIndex)index);
	if (name == NULL) {
		return NULL;
	}
	CFIndex length = CFStringGetLength(name);
	CFIndex size = CFStringGetMaximumSizeForEncoding(
		length, kCFStringEncodingUTF8) + 1;
	char *buffer = (char *)malloc((size_t)size);
	if (buffer == NULL) {
		return NULL;
	}
	if (!CFStringGetCString(name, buffer, size, kCFStringEncodingUTF8)) {
		free(buffer);
		return NULL;
	}
	return buffer;
}

static void oc_release(void *ref) {
	if (ref != NULL) {
		CFRelease((CFTypeRef)ref);
	}
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// listSystemFonts asks CoreText for the installed family names. The caller
// owns the returned CFArray, hence the release below.
func listSystemFonts() ([]string, error) {
	families := C.oc_available_families()
	defer C.oc_release(families)

	count := int(C.oc_family_count(families))
	if count == 0 {
		return nil, errors.New("sysfont: CoreText returned no font catalogue")
	}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		buffer := C.oc_family_name(families, C.long(i))
		if buffer == nil {
			// A single unreadable entry must not drop the catalogue.
			continue
		}
		out = append(out, C.GoString(buffer))
		C.free(unsafe.Pointer(buffer))
	}
	return out, nil
}
