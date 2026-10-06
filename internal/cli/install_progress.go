package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
	"github.com/itrunswap/Kivo/internal/platform"
)

type installProgress struct {
	shell       *Shell
	interactive bool
	active      bool
	lastStage   string
	lastPercent int
	last        time.Time
	restore     func()
}

func (s *Shell) newInstallProgress() *installProgress {
	p := &installProgress{shell: s, lastPercent: -1, restore: func() {}}
	if out, ok := s.outputFile(); ok {
		p.restore, p.interactive = platform.EnableTerminalOutput(out)
	}
	return p
}

func (p *installProgress) update(event core.InstallEvent) {
	if event.Stage == "complete" {
		return
	}
	line := "  " + event.Message
	if event.Stage == "download" {
		if event.Total > 0 {
			percent := int(min(int64(100), event.Downloaded*100/event.Total))
			bar := strings.Repeat("=", percent/5) + strings.Repeat("·", 20-percent/5)
			line = fmt.Sprintf("  下载 [%s] %3d%%  %s / %s", bar, percent, byteSize(event.Downloaded), byteSize(event.Total))
			if !p.interactive && p.lastStage == event.Stage && percent/10 == p.lastPercent/10 {
				return
			}
			p.lastPercent = percent
		} else {
			line = "  下载  " + byteSize(event.Downloaded) + "  · 总大小未知"
			if !p.interactive && p.lastStage == event.Stage && time.Since(p.last) < time.Second {
				return
			}
		}
		if event.BytesPerSecond > 0 {
			line += "  " + byteSize(event.BytesPerSecond) + "/s"
			if event.Total > event.Downloaded {
				line += fmt.Sprintf("  剩余约 %ds", (event.Total-event.Downloaded)/event.BytesPerSecond)
			}
		}
	}
	if p.interactive {
		if p.active && p.lastStage != event.Stage {
			fmt.Fprint(p.shell.out, "\r\n")
			p.active = false
		}
		fmt.Fprint(p.shell.out, "\r\x1b[2K"+truncateDisplay(line, p.shell.editorContentWidth()))
		p.active = true
	} else {
		fmt.Fprintln(p.shell.out, line)
	}
	p.lastStage, p.last = event.Stage, time.Now()
}

func (p *installProgress) finish() {
	if p.active {
		fmt.Fprint(p.shell.out, "\r\n")
		p.active = false
	}
	p.restore()
	p.restore = func() {}
}

func byteSize(bytes int64) string {
	if bytes >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20))
	}
	if bytes >= 1<<10 {
		return fmt.Sprintf("%.1f KiB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%d B", bytes)
}
