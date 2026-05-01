//go:build windows

package main

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func defaultTarget() string { return "C:" }

func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)
	member, err := windows.Token(0).IsMember(sid)
	return err == nil && member
}

func elevate() error {
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	args := strings.Join(append([]string{"--elevated"}, os.Args[1:]...), " ")
	verbPtr, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	cwdPtr, _ := windows.UTF16PtrFromString(cwd)
	argPtr, _ := windows.UTF16PtrFromString(args)
	return windows.ShellExecute(0, verbPtr, exePtr, argPtr, cwdPtr, 1)
}
