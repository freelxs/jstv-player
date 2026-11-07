//go:build windows

package atomicfile

import (
	"syscall"
	"unsafe"
)

func replace(src, dst string) error {
	return retryReplace(func() error {
		s, err := syscall.UTF16PtrFromString(src)
		if err != nil {
			return err
		}
		d, err := syscall.UTF16PtrFromString(dst)
		if err != nil {
			return err
		}
		proc := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
		r, _, callErr := proc.Call(uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(d)), 0x1|0x8)
		if r == 0 {
			return callErr
		}
		return nil
	}, func(err error) bool {
		code, ok := err.(syscall.Errno)
		return ok && (code == 5 || code == 32 || code == 33) // access denied, sharing violation, lock violation
	})
}
