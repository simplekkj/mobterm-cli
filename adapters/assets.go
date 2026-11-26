// Package adapters embeds the native hook templates and their compatibility policy.
package adapters

import "embed"

//go:embed minimums.json
var MinimumJSON string

//go:embed opencode/package.json opencode/tui.js pi/mobterm-jumpto.ts shared/*.js claude/*
var Native embed.FS
