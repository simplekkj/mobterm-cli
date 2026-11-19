package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

func claudeImage(_ context.Context, pid int) (string, error) {
	image := fmt.Sprintf("/proc/%d/exe", pid)
	name, err := os.Readlink(image)
	if err != nil || !claudeBinaryName(name) {
		return "", errors.New("claude_host_unverified")
	}
	return image, nil
}
