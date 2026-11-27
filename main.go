package main

import "github.com/mobterm/mobterm-cli/internal/cli"

var version = "dev"
var commit = "unknown"

func main() { cli.Main(version, commit) }
