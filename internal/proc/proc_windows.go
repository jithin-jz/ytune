//go:build windows

// Package proc holds the process handling that differs between platforms.
package proc

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// job stays open for the whole run. Windows closes it when ytune exits for any
// reason, which is what ends the child processes.
var job windows.Handle

// KillChildrenOnExit makes every process ytune starts die with it. mpv has no
// console of its own, so without this, closing the terminal window would leave
// the music playing with nothing left to stop it.
func KillChildrenOnExit() {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err == nil {
		err = windows.AssignProcessToJobObject(handle, windows.CurrentProcess())
	}
	if err != nil {
		windows.CloseHandle(handle)
		return
	}
	job = handle
}

// Hide gives a child process its own invisible console, so it can't write over
// the interface or change the settings of the console ytune is drawing in.
func Hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

var getConsoleProcessList = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")

// WaitIfOwnConsole keeps the window open if ytune was started by double-clicking
// it, because otherwise an error would vanish before anyone could read it.
func WaitIfOwnConsole() {
	var pids [2]uint32
	count, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if count == 1 {
		fmt.Print("\nPress Enter to close.")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
}
