package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7f9cf5"))
	selected    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#fadf96"))
	dimmed      = lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Italic(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#a3e635"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
)

// renderList renders the snapshot list view.
func renderList(m *Model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render(fmt.Sprintf(" omp-sync — backend %s", m.backendName)))

	if len(m.snapshots) == 0 {
		b.WriteString(dimmed.Render("  no snapshots yet"))
		b.WriteString("\n")
	} else {
		for i, s := range m.snapshots {
			id := string(s.ID)
			marker := "  "
			line := fmt.Sprintf("  %s   %s   %s",
				shortID(id),
				s.CreatedAt.Format("2006-01-02 15:04:05"),
				shortID(id))
			if i == m.cursor {
				marker = "▸ "
				b.WriteString(selected.Render(marker + line))
			} else {
				b.WriteString(marker + line)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(footerStyle.Render("  ↑/↓ navigate · p push · l pull · q quit"))
	return b.String()
}

// renderConfirm renders a yes/no prompt for a pending action.
func renderConfirm(m *Model, action string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s\n\n", titleStyle.Render(" "+action))
	b.WriteString("  Confirm? (y/n): ")
	b.WriteString("\n\n")
	b.WriteString(footerStyle.Render("  press q to cancel"))
	return b.String()
}

// renderActionResult renders the captured output of a TUI-triggered CLI action.
func renderActionResult(m *Model) string {
	var b strings.Builder
	r := m.LastAction
	fmt.Fprintf(&b, "\n  %s\n\n", titleStyle.Render(fmt.Sprintf(" %s — exit %d (%s)",
		r.Op, r.ExitCode, r.Duration.Round(time.Millisecond))))
	if r.Output != "" {
		b.WriteString(r.Output)
		if !strings.HasSuffix(r.Output, "\n") {
			b.WriteString("\n")
		}
	}
	if r.ErrOutput != "" {
		fmt.Fprintf(&b, "%s%s\n", errStyle.Render(" stderr: "), r.ErrOutput)
	}
	if r.ExitCode == 0 {
		fmt.Fprintf(&b, "\n  %s\n", okStyle.Render("OK"))
	} else {
		fmt.Fprintf(&b, "\n  %s\n", errStyle.Render("FAILED"))
	}
	b.WriteString("\n")
	b.WriteString(footerStyle.Render("  press q to quit"))
	return b.String()
}

// shortID returns the first 10 chars of s, or s itself if shorter.
func shortID(s string) string {
	const width = 10
	if len(s) <= width {
		return s
	}
	return s[:width]
}

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// keep imports referenced.
var _ = strings.Builder{}
var _ timeAlias

type timeAlias = struct{}
