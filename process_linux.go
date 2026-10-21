package main

import (
	"errors"
	"fmt"
	"strings"
)

func processIdentity(pid int) (string, error) {
	boot, err := secureRead("/proc/sys/kernel/random/boot_id", 128)
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return "", errors.New("process_boot_identity_unavailable")
	}
	stat, err := secureRead(fmt.Sprintf("/proc/%d/stat", pid), 8192)
	if err != nil {
		return "", errors.New("process_birth_unavailable")
	}
	// comm can itself contain spaces and parentheses. Only read starttime (field 22).
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return "", errors.New("process_birth_unavailable")
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 20 || !intText(fields[19]) || fields[19] == "0" {
		return "", errors.New("process_birth_unavailable")
	}
	return "tmux-proc-v1:" + digest(fmt.Sprintf("linux\n%s\n%d\n%s", strings.ToLower(strings.TrimSpace(string(boot))), pid, fields[19])), nil
}
