//go:build windows

package mesh

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createNoWindow                    = 0x08000000
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInfo struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IOInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type windowsJobManager struct {
	once   sync.Once
	handle uintptr
	err    error
}

var (
	kernel32ProcCreateJobObjectW         = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateJobObjectW")
	kernel32ProcSetInformationJobObject  = syscall.NewLazyDLL("kernel32.dll").NewProc("SetInformationJobObject")
	kernel32ProcAssignProcessToJobObject = syscall.NewLazyDLL("kernel32.dll").NewProc("AssignProcessToJobObject")
	kernel32ProcOpenProcess              = syscall.NewLazyDLL("kernel32.dll").NewProc("OpenProcess")
	kernel32ProcCloseHandle              = syscall.NewLazyDLL("kernel32.dll").NewProc("CloseHandle")
	managedProcessJob                    windowsJobManager
)

func configureManagedProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

func registerManagedProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	jobHandle, err := managedProcessJob.ensure()
	if err != nil {
		return err
	}

	processHandle, err := openProcessHandle(cmd.Process.Pid)
	if err != nil {
		return err
	}
	defer closeWindowsHandle(processHandle)

	r1, _, callErr := kernel32ProcAssignProcessToJobObject.Call(jobHandle, processHandle)
	if r1 == 0 {
		return fmt.Errorf("assign process to job object: %w", normalizeWindowsCallErr(callErr))
	}
	return nil
}

func (m *windowsJobManager) ensure() (uintptr, error) {
	m.once.Do(func() {
		handle, _, callErr := kernel32ProcCreateJobObjectW.Call(0, 0)
		if handle == 0 {
			m.err = fmt.Errorf("create job object: %w", normalizeWindowsCallErr(callErr))
			return
		}

		info := jobObjectExtendedLimitInfo{}
		info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
		r1, _, callErr := kernel32ProcSetInformationJobObject.Call(
			handle,
			jobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uintptr(uint32(unsafe.Sizeof(info))),
		)
		if r1 == 0 {
			closeWindowsHandle(handle)
			m.err = fmt.Errorf("set job object kill-on-close: %w", normalizeWindowsCallErr(callErr))
			return
		}

		m.handle = handle
	})
	return m.handle, m.err
}

func openProcessHandle(pid int) (uintptr, error) {
	r1, _, callErr := kernel32ProcOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(uint32(pid)))
	if r1 == 0 {
		return 0, fmt.Errorf("open process PID %d: %w", pid, normalizeWindowsCallErr(callErr))
	}
	return r1, nil
}

func closeWindowsHandle(handle uintptr) {
	if handle != 0 {
		_, _, _ = kernel32ProcCloseHandle.Call(handle)
	}
}

func normalizeWindowsCallErr(err error) error {
	if errno, ok := err.(syscall.Errno); ok && errno == 0 {
		return syscall.EINVAL
	}
	return err
}
