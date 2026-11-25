package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// The upgrade goroutine owns begin and finish. Its HTTP reader accumulates
// bytes while the renderer samples them at most eight times per second.
type upgradeProgress struct {
	writer io.Writer
	caps   func() terminalCaps
	mode   string
	stop   chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	stage   string
	short   string
	started time.Time
	body    bool
	read    int64
	total   int64

	// Renderer state, also protected by mu.
	visible    bool
	lastLine   string
	lastWidth  int
	plainStage string
}

const progressInterval = 125 * time.Millisecond

func newUpgradeProgress(w io.Writer, caps func() terminalCaps, mode string) *upgradeProgress {
	return &upgradeProgress{writer: w, caps: caps, mode: mode}
}

func (p *upgradeProgress) currentStage() string {
	if p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.stage != "" {
			return p.stage
		}
	}
	return "Preparing update"
}

func (p *upgradeProgress) begin(stage, short string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stage, p.short, p.started = stage, short, time.Now()
	p.body, p.read, p.total = false, 0, 0
	switch p.mode {
	case "never":
		return
	case "plain":
		p.render(p.started)
	default:
		if p.stop == nil {
			p.stop, p.done = make(chan struct{}), make(chan struct{})
			go p.refresh()
		} else if p.visible || p.plainStage != "" {
			// Once visible, show each remaining stage even if it finishes between ticks.
			p.render(p.started)
		}
	}
}

func (p *upgradeProgress) startBody(total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.body, p.total = true, total
}

func (p *upgradeProgress) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.read += int64(len(b))
	p.mu.Unlock()
	return len(b), nil
}

func (p *upgradeProgress) refresh() {
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	defer close(p.done)
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			p.render(time.Now())
			p.mu.Unlock()
		}
	}
}

// The cursor rests at the beginning of the temporary line between frames.
// After a shrink, clearing to the end of the display also removes its reflowed tail.
func (p *upgradeProgress) erase(width int) {
	if p.visible {
		clear := "\r\x1b[K"
		if width < p.lastWidth {
			clear = "\r\x1b[J"
		}
		fmt.Fprint(p.writer, clear)
		p.visible = false
	}
}

// Called with mu held to serialize stage transitions and periodic refreshes.
func (p *upgradeProgress) render(now time.Time) {
	caps := p.caps()
	if p.mode == "plain" || !caps.Dynamic {
		p.erase(caps.Width)
		if p.plainStage != p.stage {
			fmt.Fprintln(p.writer, wrapCells(p.stage, max(1, caps.Width)))
			p.plainStage = p.stage
		}
		return
	}
	line := p.frame(max(1, caps.Width-1), caps.Unicode, now.Sub(p.started))
	if p.visible && line == p.lastLine && caps.Width == p.lastWidth {
		return
	}
	p.erase(caps.Width)
	fmt.Fprintf(p.writer, "%s\r", caps.style("title", line))
	p.visible, p.lastLine, p.lastWidth = true, line, caps.Width
}

func (p *upgradeProgress) finish() {
	if p == nil {
		return
	}
	if p.stop != nil {
		close(p.stop)
		<-p.done
		p.stop = nil
	}
	if p.visible {
		p.erase(p.caps().Width)
	}
}

func progressBytes(count int64) string {
	switch {
	case count >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(count)/(1024*1024))
	case count >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(count)/1024)
	default:
		return fmt.Sprintf("%d B", count)
	}
}

// Every generated character occupies one terminal cell. Drop secondary
// information first, then use compact stage names on phone-sized terminals.
func (p *upgradeProgress) frame(width int, unicode bool, elapsed time.Duration) string {
	spin := []rune("|/-\\")
	if unicode {
		spin = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	}
	marker := string(spin[int(elapsed/progressInterval)%len(spin)])
	var detail, core string
	if p.body {
		core = progressBytes(p.read)
		detail = core + "  " + elapsed.Truncate(time.Second).String()
		if p.total > 0 && p.read <= p.total {
			core = fmt.Sprintf("%d%%", 100*p.read/p.total)
			detail = core + "  " + progressBytes(p.read) + " / " + progressBytes(p.total)
		}
	}
	for _, line := range []string{
		strings.TrimRight(marker+" "+p.stage+"  "+detail, " "),
		strings.TrimRight(marker+" "+p.short+"  "+detail, " "),
		strings.TrimRight(p.short+" "+core, " "),
	} {
		if len([]rune(line)) <= width {
			return line
		}
	}
	// Everything remaining is ASCII. Preserve the core counter even when
	// only a prefix of the compact stage name fits.
	if core != "" {
		room := width - len(core) - 1
		if room > 0 {
			return p.short[:min(room, len(p.short))] + " " + core
		}
		if len(core) <= width {
			return core
		}
	}
	return p.short[:min(width, len(p.short))]
}
