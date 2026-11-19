package main

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Only bounded assistant text enters durable state. This is a plain-text
// excerpt, never an interpretation of task success or an authorization input.
func cleanMessageSummary(text string) string {
	if len(text) > 65536 {
		text = text[:65536]
	}
	// Fenced code and attachment-only blocks are not prose. Select the first
	// prose paragraph, without joining separate paragraphs or code into it.
	var lines []string
	fence := ""
	skipBlock := false
	thought := ""
	for _, line := range strings.Split(strings.ReplaceAll(cleanTerminalText(text), "\ufffd", ""), "\n") {
		trim := strings.TrimSpace(line)
		if thought != "" {
			if strings.Contains(trim, thought) {
				thought = ""
			}
			continue
		}
		if strings.HasPrefix(trim, "<think>") || strings.HasPrefix(trim, "<analysis>") {
			closing := "</think>"
			if strings.HasPrefix(trim, "<analysis>") {
				closing = "</analysis>"
			}
			if !strings.Contains(trim, closing) {
				thought = closing
			}
			continue
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			if len(lines) > 0 {
				break
			}
			if fence == "" {
				fence = trim[:3]
			} else if strings.HasPrefix(trim, fence) {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if trim == "" {
			skipBlock = false
			if len(lines) > 0 {
				break
			}
			continue
		}
		if skipBlock {
			continue
		}
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(trim, "![") || strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[") {
			skipBlock = true
			if len(lines) > 0 {
				break
			}
			continue
		}
		lines = append(lines, trim)
	}
	return truncateMessageSummary(strings.Join(strings.Fields(strings.Join(lines, " ")), " "), 120, 512)
}

// Keep common combining marks, emoji modifiers, ZWJ chains and flag pairs
// together. A cluster which would exceed either budget is omitted as a whole.
func truncateMessageSummary(text string, maxRunes, maxBytes int) string {
	runes := []rune(text)
	end, count, size := 0, 0, 0
	for i := 0; i < len(runes); {
		j := i + 1
		if runes[i] >= 0x1f1e6 && runes[i] <= 0x1f1ff && j < len(runes) && runes[j] >= 0x1f1e6 && runes[j] <= 0x1f1ff {
			j++
		}
		for j < len(runes) {
			r := runes[j]
			if unicode.Is(unicode.M, r) || r >= 0x1f3fb && r <= 0x1f3ff || r >= 0xe0020 && r <= 0xe007f {
				j++
				continue
			}
			if r == 0x200d && j+1 < len(runes) {
				j += 2
				continue
			}
			break
		}
		cluster := string(runes[i:j])
		if count+j-i > maxRunes || size+len(cluster) > maxBytes {
			break
		}
		count += j - i
		size += len(cluster)
		end = j
		i = j
	}
	return strings.TrimSpace(string(runes[:end]))
}

// Include badge and Foundation's conservative slash escaping, matching the
// API/NSE contract. No summary is exposed outside authenticated ciphertext.
func notificationPayloadSize(e Envelope) int {
	payload := map[string]any{"aps": map[string]any{"alert": map[string]any{"title": "MobTerm", "loc-key": "agentNotificationNeedsInputAlert"}, "sound": "default", "mutable-content": 1, "badge": 2147483647}, "mobterm": e}
	raw, _ := json.Marshal(payload)
	return len(raw) + strings.Count(string(raw), "/")
}

func cleanTerminalText(text string) string {
	var b strings.Builder
	state := 0 // plain, ESC, CSI, OSC, OSC-ESC
	for _, r := range text {
		switch state {
		case 1:
			if r == '[' {
				state = 2
			} else if strings.ContainsRune("]PX^_", r) {
				state = 3
			} else {
				state = 0
			}
			continue
		case 2:
			if r >= 0x40 && r <= 0x7e {
				state = 0
			}
			continue
		case 3:
			if r == 7 || r == 0x9c {
				state = 0
			} else if r == 27 {
				state = 4
			}
			continue
		case 4:
			if r == '\\' {
				state = 0
			} else {
				state = 3
			}
			continue
		}
		if r == 27 {
			state = 1
			continue
		}
		if r == 0x9b {
			state = 2
			continue
		}
		if r == 0x9d || r == 0x90 || r == 0x98 || r == 0x9e || r == 0x9f {
			state = 3
			continue
		}
		if r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0x200e || r == 0x200f || r == 0x061c {
			continue
		}
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
