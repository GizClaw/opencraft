//go:build darwin

package secrets

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

// All helpers exchange CoreFoundation objects as void* so cgo maps
// every value to unsafe.Pointer (CF types are distinct void* typedefs
// that cgo would otherwise expose as incompatible named uintptr types).

static void *kc_dict(void) {
	return CFDictionaryCreateMutable(
		kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
}

static void kc_dict_set(void *d, void *key, void *value) {
	CFDictionarySetValue((CFMutableDictionaryRef)d,
		(CFStringRef)key, (CFTypeRef)value);
}

static void *kc_class_generic(void) {
	return (void *)kSecClassGenericPassword;
}

static void *kc_class(void) {
	return (void *)kSecClass;
}

static void *kc_attr_service(void) {
	return (void *)kSecAttrService;
}

static void *kc_attr_account(void) {
	return (void *)kSecAttrAccount;
}

static void *kc_value_data(void) {
	return (void *)kSecValueData;
}

static void *kc_attr_return_data(void) {
	return (void *)kSecReturnData;
}

static void *kc_true(void) {
	return (void *)kCFBooleanTrue;
}

static void *kc_string(const char *s) {
	return CFStringCreateWithCString(
		kCFAllocatorDefault, s, kCFStringEncodingUTF8);
}

static void *kc_data(const void *bytes, CFIndex len) {
	return CFDataCreate(kCFAllocatorDefault, bytes, len);
}

static const void *kc_data_bytes(void *d) {
	return CFDataGetBytePtr((CFDataRef)d);
}

static CFIndex kc_data_len(void *d) {
	return CFDataGetLength((CFDataRef)d);
}

static void kc_release(void *ref) {
	if (ref != NULL) {
		CFRelease((CFTypeRef)ref);
	}
}

static int kc_add(void *attrs) {
	return SecItemAdd((CFDictionaryRef)attrs, NULL);
}

static int kc_update(void *query, void *attrs) {
	return SecItemUpdate((CFDictionaryRef)query, (CFDictionaryRef)attrs);
}

static int kc_find(void *query, void **result) {
	return SecItemCopyMatching((CFDictionaryRef)query, (CFTypeRef *)result);
}

static int kc_delete(void *query) {
	return SecItemDelete((CFDictionaryRef)query);
}
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

// keychainBackend stores Generic Password items in the login Keychain
// through the Security framework. Unlike security(1), it never spawns a
// subprocess or reads a terminal, so a GUI app can never block on an
// interactive prompt.
type keychainBackend struct{ service string }

// Available reports whether the Security framework is usable. It always
// is on macOS; the value exists to satisfy the backend contract.
func (k *keychainBackend) Available() bool { return true }

// query builds the generic-password search/insert dictionary for one
// service/account pair plus the service/account strings that must be
// released alongside it.
func (k *keychainBackend) query(name string) (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) {
	service := C.kc_string(C.CString(k.service))
	account := C.kc_string(C.CString(name))
	q := C.kc_dict()
	C.kc_dict_set(q, C.kc_class(), C.kc_class_generic())
	C.kc_dict_set(q, C.kc_attr_service(), service)
	C.kc_dict_set(q, C.kc_attr_account(), account)
	return q, service, account
}

func releaseCF(refs ...unsafe.Pointer) {
	for _, ref := range refs {
		C.kc_release(ref)
	}
}

func (k *keychainBackend) Get(_ context.Context, name string) (string, bool, error) {
	q, service, account := k.query(name)
	defer releaseCF(q, service, account)

	var result unsafe.Pointer
	C.kc_dict_set(q, C.kc_attr_return_data(), C.kc_true())
	status := C.kc_find(q, &result)
	if status == C.errSecItemNotFound {
		return "", false, nil
	}
	if status != C.errSecSuccess {
		return "", false, fmt.Errorf(
			"opencraft secrets: keychain lookup %q: %d", name, int(status))
	}
	defer releaseCF(result)

	length := C.kc_data_len(result)
	if length <= 0 {
		return "", true, nil
	}
	value := C.GoBytes(C.kc_data_bytes(result), C.int(length))
	return string(value), true, nil
}

func (k *keychainBackend) Set(_ context.Context, name, value string) error {
	q, service, account := k.query(name)
	defer releaseCF(q, service, account)

	cstr := C.CString(value)
	defer C.free(unsafe.Pointer(cstr))
	data := C.kc_data(unsafe.Pointer(cstr), C.CFIndex(len(value)))
	defer releaseCF(data)
	C.kc_dict_set(q, C.kc_value_data(), data)

	status := C.kc_add(q)
	if status == C.errSecDuplicateItem {
		attrs := C.kc_dict()
		defer releaseCF(attrs)
		C.kc_dict_set(attrs, C.kc_value_data(), data)
		status = C.kc_update(q, attrs)
	}
	if status != C.errSecSuccess {
		return fmt.Errorf(
			"opencraft secrets: keychain add %q: %d", name, int(status))
	}
	return nil
}

func (k *keychainBackend) Delete(_ context.Context, name string) error {
	q, service, account := k.query(name)
	defer releaseCF(q, service, account)

	status := C.kc_delete(q)
	if status != C.errSecSuccess && status != C.errSecItemNotFound {
		return fmt.Errorf(
			"opencraft secrets: keychain delete %q: %d", name, int(status))
	}
	return nil
}
