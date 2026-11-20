// Synthetic dispatch host for local CLI measurements. No Agent configuration,
// model, prompt, shell evaluation or terminal routing is involved here.
package main

import (
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "exec" {
		os.Exit(2)
	}
	command := exec.Command(os.Args[2], os.Args[3:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
