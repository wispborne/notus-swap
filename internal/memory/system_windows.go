package memory

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var globalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// memoryStatusEx is Windows' MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// System returns used and total RAM in bytes. On Windows it asks the system,
// and meminfo is ignored. Used is the total minus what Windows counts as
// available, which includes standby cache, as on Linux.
func System(meminfo string) (used, total *int64) {
	s := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if ok, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&s))); ok == 0 {
		return nil, nil
	}
	if s.totalPhys == 0 || s.availPhys > s.totalPhys {
		return nil, nil
	}
	u, t := int64(s.totalPhys-s.availPhys), int64(s.totalPhys)
	return &u, &t
}
