package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/clipperhouse/displaywidth"
	"golang.org/x/term"
)

type displayOptions struct {
	color, progress string
	plain           bool
	helpPath        []string
}

// Options with a value use the documented --name=value form. Leave invalid
// options in argv for the command's normal argument error and exit status.
func (o *displayOptions) take(arg string) bool {
	switch arg {
	case "--plain":
		o.plain = true
	case "--color=auto", "--color=always", "--color=never":
		o.color = strings.TrimPrefix(arg, "--color=")
	case "--progress=auto", "--progress=plain", "--progress=never":
		o.progress = strings.TrimPrefix(arg, "--progress=")
	default:
		return false
	}
	return true
}

// Recognize display flags on human commands and help on public maintenance
// commands. Agent invocations and framed protocols keep their own parsers.
func resultOptions(args []string) ([]string, displayOptions, bool) {
	var options displayOptions
	start := 1
	maintenance := false
	switch args[0] {
	case "hooks":
		if len(args) < 2 || args[1] != "install" && args[1] != "uninstall" {
			return args, options, false
		}
		start = 2
	case "outbox", "pairs":
		if len(args) < 2 || args[1] == "retire" || !slices.Contains(args, "--help") && !slices.Contains(args, "-h") {
			return args, options, false
		}
		maintenance = true
		start = 2
	case "status", "test", "upgrade":
	default:
		return args, options, false
	}
	human := true
	clean := append([]string(nil), args[:start]...)
	for i := start; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			clean = append(clean, args[i:]...)
			break
		}
		if options.take(arg) {
			continue
		}
		if arg == "--help" || arg == "-h" {
			options.helpPath = args[:start]
		}
		clean = append(clean, arg)
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch name {
		case "pair", "version", "agent":
			if !hasValue && i+1 < len(args) {
				i++
				clean = append(clean, args[i])
			}
		case "json", "agents-json", "installation-json", "read-only", "stdin-frame":
			human = false
		}
	}
	if !human {
		options.helpPath = nil
	}
	if maintenance {
		return args, options, false
	}
	return clean, options, human
}

// Recognize a help path only when all arguments belong to help. In particular
// strings passed as flag values, Hook payloads and frame invocations stay opaque.
func handleHelp(args []string, out, diagnostic io.Writer) (int, bool) {
	explicit := len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h"
	if !explicit && !slices.Contains(args, "-h") && !slices.Contains(args, "--help") {
		return 0, false
	}
	var options displayOptions
	var path []string
	for i, arg := range args {
		switch {
		case i == 0 && arg == "help", arg == "-h", arg == "--help", options.take(arg):
		case strings.HasPrefix(arg, "-"):
			if !explicit {
				return 0, false
			}
			fmt.Fprintln(diagnostic, "mobterm-cli: invalid help option")
			return 1, true
		default:
			path = append(path, arg)
		}
	}
	if renderHelp(out, detectTerminal(out, options), path) {
		return 0, true
	}
	if !explicit {
		return 0, false
	}
	fmt.Fprintln(diagnostic, "mobterm-cli: unknown help topic")
	return 1, true
}

type terminalCaps struct {
	TTY, Unicode, Dynamic, Hyperlinks bool
	Width, ColorDepth                 int
}

// These TERM families use ANSI EL/ED, the two operations our live line
// needs. A color-depth hint by itself says nothing about cursor control.
func ansiSupport(terminal, colorTerm string) (erase, color bool) {
	family := terminal
	if end := strings.IndexAny(terminal, "-."); end >= 0 {
		family = terminal[:end]
	}
	switch family {
	case "dumb", "unknown", "vt52":
		return false, false
	case "vt100", "vt101", "vt102", "vt125", "vt132", "vt220", "vt240", "vt320", "vt420", "vt520":
		return true, false
	case "xterm", "rxvt", "screen", "tmux", "ansi", "linux", "cygwin", "st", "putty",
		"alacritty", "foot", "kitty", "wezterm", "ghostty", "konsole", "vte", "contour", "eterm":
		return true, !strings.HasSuffix(terminal, "-mono")
	}
	return false, !strings.HasSuffix(terminal, "-mono") && (colorTerm == "truecolor" || colorTerm == "24bit" ||
		strings.HasSuffix(terminal, "-256color") || strings.HasSuffix(terminal, "-direct"))
}

func detectTerminal(w io.Writer, options displayOptions) terminalCaps {
	c := terminalCaps{Width: 80}
	if file, ok := w.(*os.File); ok && file != nil {
		fd := int(file.Fd())
		c.TTY = term.IsTerminal(fd)
		if c.TTY {
			if width, _, err := term.GetSize(fd); err == nil && width > 0 {
				c.Width = width
			}
		}
	}
	if options.plain {
		return c
	}
	terminal := strings.ToLower(os.Getenv("TERM"))
	colorTerm := strings.ToLower(os.Getenv("COLORTERM"))
	erase, hasColor := ansiSupport(terminal, colorTerm)
	ansiTerminal := c.TTY && erase
	c.Dynamic = ansiTerminal && os.Getenv("CI") == "" && options.progress != "plain" && options.progress != "never"
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if locale := os.Getenv(key); locale != "" {
			c.Unicode = strings.Contains(strings.ReplaceAll(strings.ToLower(locale), "-", ""), "utf8")
			break
		}
	}
	color := c.TTY && hasColor
	switch {
	case options.color == "always":
		color = true
	case options.color == "never" || os.Getenv("NO_COLOR") != "":
		color = false
	case os.Getenv("CLICOLOR_FORCE") != "" && os.Getenv("CLICOLOR_FORCE") != "0":
		color = true
	case os.Getenv("CLICOLOR") == "0":
		color = false
	}
	if color {
		switch {
		case colorTerm == "truecolor" || colorTerm == "24bit" || strings.HasSuffix(terminal, "-direct"):
			c.ColorDepth = 24
		case strings.Contains(terminal, "256color"):
			c.ColorDepth = 8
		default:
			c.ColorDepth = 4
		}
	}
	// Only enable OSC 8 on a recognized local, directly connected terminal.
	if ansiTerminal && os.Getenv("TMUX") == "" && os.Getenv("STY") == "" && os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_TTY") == "" && !strings.HasPrefix(terminal, "screen") && !strings.HasPrefix(terminal, "tmux") {
		switch os.Getenv("TERM_PROGRAM") {
		case "iTerm.app", "WezTerm", "ghostty":
			c.Hyperlinks = true
		}
	}
	return c
}

func (c terminalCaps) link(url string) string {
	if c.Hyperlinks {
		return "\x1b]8;;" + url + "\x1b\\" + url + "\x1b]8;;\x1b\\"
	}
	return url
}

func (c terminalCaps) style(role, text string) string {
	if c.ColorDepth == 0 {
		return text
	}
	// Status colors use the terminal theme. The heading accent uses the
	// nearest palette entry at each available depth.
	var code string
	switch role {
	case "title":
		switch c.ColorDepth {
		case 24:
			code = "1;38;2;36;142;151"
		case 8:
			code = "1;38;5;30"
		default:
			code = "1;36"
		}
	case "success":
		code = "32"
	case "warning":
		code = "33"
	case "error":
		code = "31"
	case "hint":
		code = "36"
	default:
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

var terminalWidth = displaywidth.Options{ControlSequences: true}

func cellWidth(s string) int { return terminalWidth.String(s) }

// Wrap at a space when possible and split long words on grapheme boundaries.
// This iterator works on demand, including for ANSI sequences of zero width.
func wrapCells(s string, width int) string {
	width = max(1, width)
	var out strings.Builder
	start, offset, wordStart := 0, 0, 0
	lineWidth, wordWidth := 0, 0
	graphemes := terminalWidth.StringGraphemes(s)
	for graphemes.Next() {
		g, cells := graphemes.Value(), graphemes.Width()
		if g == "\n" {
			offset += len(g)
			out.WriteString(s[start:offset])
			start, wordStart, lineWidth, wordWidth = offset, offset, 0, 0
			continue
		}
		if g == " " {
			wordStart, wordWidth = offset+1, 0
		} else if lineWidth+cells > width && lineWidth > 0 {
			if wordStart > start {
				out.WriteString(strings.TrimRight(s[start:wordStart], " "))
				out.WriteByte('\n')
				start, lineWidth = wordStart, wordWidth
			}
			if lineWidth+cells > width && offset > start {
				out.WriteString(s[start:offset])
				out.WriteByte('\n')
				start, wordStart, lineWidth, wordWidth = offset, offset, 0, 0
			}
		}
		offset += len(g)
		lineWidth += cells
		if g != " " {
			wordWidth += cells
		}
	}
	out.WriteString(s[start:])
	return out.String()
}

// Values interpolated into human output include paths and upstream error text.
func terminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control) {
			return '?'
		}
		return r
	}, s)
}

func writeHumanError(w io.Writer, c terminalCaps, args []string, progress *upgradeProgress, err error) {
	action := args[0]
	hint := "Run mobterm-cli " + action + " --help for usage."
	if action == "hooks" && len(args) > 1 {
		action += " " + args[1]
	}
	if progress != nil {
		action = progress.currentStage()
		hint = "Check the requested release and run mobterm-cli version to inspect the installed executable."
	}
	fmt.Fprintln(w, c.style("error", wrapCells("Error: "+action+": "+terminalText(err.Error()), c.Width)))
	fmt.Fprintln(w, c.style("hint", wrapCells("hint: "+hint, c.Width)))
}
