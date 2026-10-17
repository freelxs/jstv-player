//go:build windows

package catvod

import (
	"syscall"
	"unsafe"
)

const moveFileReplaceExisting = 0x1
const moveFileWriteThrough = 0x8

func replaceFile(src, dst string) error {
	s, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	d, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("MoveFileExW")
	r1, _, e1 := proc.Call(uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(d)), moveFileReplaceExisting|moveFileWriteThrough)
	if r1 == 0 {
		return e1
	}
	return nil
}
