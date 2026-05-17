package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type palette struct {
	title     lipgloss.Style
	subtle    lipgloss.Style
	panel     lipgloss.Style
	focus     lipgloss.Style
	row       lipgloss.Style
	selected  lipgloss.Style
	good      lipgloss.Style
	warn      lipgloss.Style
	bad       lipgloss.Style
	pill      lipgloss.Style
	command   lipgloss.Style
	disabled  lipgloss.Style
	highlight lipgloss.Style
}

func render(m Model) string {
	p := styles(m.noColor)
	if m.err != nil {
		return p.panel.Render("TUI error: "+m.err.Error()) + "\n"
	}
	width := m.width
	if width < 80 {
		width = 80
	}
	leftW := clamp(width/3, 28, 42)
	rightW := width - leftW - 6
	if rightW < 44 {
		rightW = 44
	}
	header := renderHeader(m, p, width)
	left := lipgloss.JoinVertical(lipgloss.Left,
		renderServices(m, p, leftW),
		renderSagas(m, p, leftW),
	)
	right := lipgloss.JoinVertical(lipgloss.Left,
		renderServiceDetail(m, p, rightW),
		renderEvents(m, p, rightW),
		renderCommandPalette(m, p, rightW),
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
	footer := renderFooter(m, p, width)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer) + "\n"
}

func renderHeader(m Model, p palette, width int) string {
	status := "ready"
	if m.loading {
		status = "refreshing"
	}
	meta := fmt.Sprintf("%s | %s | source %s | freshness %ds", firstNonEmpty(string(m.dashboard.Status.Mode), "mode"), firstNonEmpty(m.dashboard.Status.Env, "env"), firstNonEmpty(m.dashboard.Source, m.dashboard.Freshness.Source), m.dashboard.Freshness.FreshnessSeconds)
	if m.readOnly {
		meta += " | read-only"
	}
	line := p.subtle.Render(meta)
	return p.title.Width(width).Render("Skiff Operations") + "\n" + p.pill.Render(status) + " " + line
}

func renderServices(m Model, p palette, width int) string {
	lines := []string{p.highlight.Render("Services")}
	if len(m.dashboard.Status.Services) == 0 {
		lines = append(lines, p.subtle.Render("No services in object state"))
	}
	for i, service := range m.dashboard.Status.Services {
		prefix := "  "
		style := p.row
		if m.focus == focusServices && i == m.selected {
			prefix = "> "
			style = p.selected
		}
		health := healthStyle(p, service.Health).Render(firstNonEmpty(service.Health, "unknown"))
		release := firstNonEmpty(service.DesiredRelease, "<none>")
		line := fmt.Sprintf("%s%s  %s  %s", prefix, service.Service, health, release)
		if service.OperationID != "" {
			line += "  " + p.subtle.Render(service.OperationID+":"+firstNonEmpty(service.OperationState, "running"))
		}
		lines = append(lines, style.Width(width-4).Render(line))
	}
	return panelForFocus(m, p, focusServices).Width(width).Render(strings.Join(lines, "\n"))
}

func renderSagas(m Model, p palette, width int) string {
	lines := []string{p.highlight.Render("Active Sagas")}
	if len(m.dashboard.Sagas) == 0 {
		lines = append(lines, p.subtle.Render("No active saga controls"))
	}
	for i, saga := range m.dashboard.Sagas {
		prefix := "  "
		style := p.row
		if m.focus == focusSagas && i == m.selected {
			prefix = "> "
			style = p.selected
		}
		status := sagaStatusStyle(p, saga.Status).Render(string(saga.Status))
		step := ""
		if len(saga.CurrentSteps) > 0 {
			step = "  " + p.subtle.Render(strings.Join(saga.CurrentSteps, ","))
		}
		lines = append(lines, style.Width(width-4).Render(fmt.Sprintf("%s%s  %s%s", prefix, saga.SagaID, status, step)))
	}
	return panelForFocus(m, p, focusSagas).Width(width).Render(strings.Join(lines, "\n"))
}

func renderServiceDetail(m Model, p palette, width int) string {
	service := m.selectedService()
	lines := []string{p.highlight.Render("Selected Service")}
	if service.Service == "" {
		lines = append(lines, p.subtle.Render("Select a service to inspect rollout, logs, metrics, and recovery actions."))
		return p.panel.Width(width).Render(strings.Join(lines, "\n"))
	}
	lines = append(lines,
		fmt.Sprintf("%s  %s", p.title.Render(service.Service), healthStyle(p, service.Health).Render(firstNonEmpty(service.Health, "unknown"))),
		fmt.Sprintf("desired %s   stable %s", p.command.Render(firstNonEmpty(service.DesiredRelease, "<none>")), p.command.Render(firstNonEmpty(service.StableRelease, "<none>"))),
		dependencyLine(p, "capacity", service.Capacity),
		dependencyLine(p, "target health", service.TargetHealth),
		dependencyLine(p, "logs", service.Logs),
		dependencyLine(p, "metrics", service.Metrics),
	)
	if service.Rollout != nil {
		lines = append(lines, fmt.Sprintf("rollout %s  provider %s", p.warn.Render(firstNonEmpty(service.Rollout.Status, "unknown")), p.command.Render(firstNonEmpty(service.Rollout.ProviderID, "<none>"))))
	}
	if len(service.Findings) > 0 {
		lines = append(lines, p.warn.Render("findings"))
		for _, finding := range service.Findings {
			lines = append(lines, "  "+finding.Code+"  "+finding.Summary)
		}
	}
	return p.panel.Width(width).Render(strings.Join(lines, "\n"))
}

func renderEvents(m Model, p palette, width int) string {
	lines := []string{p.highlight.Render("Recent Events")}
	if len(m.dashboard.Events) == 0 {
		lines = append(lines, p.subtle.Render("No recent events"))
	}
	for i, event := range m.dashboard.Events {
		style := p.row
		prefix := "  "
		if m.focus == focusEvents && i == m.selected {
			style = p.selected
			prefix = "> "
		}
		subject := event.Subject.Kind + ":" + event.Subject.Name
		lines = append(lines, style.Width(width-4).Render(fmt.Sprintf("%s%s  %s  %s", prefix, trimTime(event.Time), p.command.Render(event.Type), subject)))
	}
	return panelForFocus(m, p, focusEvents).Width(width).Render(strings.Join(lines, "\n"))
}

func renderCommandPalette(m Model, p palette, width int) string {
	title := p.highlight.Render("Command Palette")
	if m.action == nil {
		help := "d doctor   l logs   m metrics   e events   x saga   b rollback   a approve"
		return p.panel.Width(width).Render(title + "\n" + p.subtle.Render(help))
	}
	action := *m.action
	state := "ready"
	style := p.good
	if action.Mutating {
		state = fmt.Sprintf("mutating | risk %s | %s", firstNonEmpty(string(action.Risk), "medium"), firstNonEmpty(string(action.Reversibility), "compensatable"))
		style = p.warn
	}
	if !action.Allowed {
		state = "blocked by read-only mode"
		style = p.disabled
	}
	md := fmt.Sprintf("**%s**  %s\n\n`%s`", action.Label, state, action.Command)
	if rendered, err := renderMarkdown(md, width-6, m.noColor); err == nil && strings.TrimSpace(rendered) != "" {
		return p.panel.Width(width).Render(title + "\n" + strings.TrimSpace(rendered))
	}
	return p.panel.Width(width).Render(title + "\n" + style.Render(action.Label+"  "+state) + "\n" + p.command.Render(action.Command))
}

func renderFooter(m Model, p palette, width int) string {
	return p.subtle.Width(width).Render("tab switch pane | j/k move | r refresh | q quit")
}

func styles(noColor bool) palette {
	if noColor {
		base := lipgloss.NewStyle()
		return palette{
			title:     base.Bold(true),
			subtle:    base.Faint(true),
			panel:     base.Border(lipgloss.NormalBorder()).Padding(0, 1),
			focus:     base.Border(lipgloss.DoubleBorder()).Padding(0, 1),
			row:       base,
			selected:  base.Bold(true),
			good:      base,
			warn:      base,
			bad:       base,
			pill:      base.Bold(true),
			command:   base,
			disabled:  base.Faint(true),
			highlight: base.Bold(true),
		}
	}
	return palette{
		title:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("45")).Padding(0, 1),
		subtle:    lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		panel:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("245")).Padding(0, 1),
		focus:     lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(lipgloss.Color("220")).Padding(0, 1),
		row:       lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		selected:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("30")),
		good:      lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		warn:      lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
		bad:       lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		pill:      lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("43")).Bold(true).Padding(0, 1),
		command:   lipgloss.NewStyle().Foreground(lipgloss.Color("81")),
		disabled:  lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Strikethrough(true),
		highlight: lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true),
	}
}

func panelForFocus(m Model, p palette, focus string) lipgloss.Style {
	if m.focus == focus {
		return p.focus
	}
	return p.panel
}

func healthStyle(p palette, health string) lipgloss.Style {
	switch health {
	case "healthy", "serving", "ok":
		return p.good
	case "failed", "critical", "unhealthy":
		return p.bad
	default:
		return p.warn
	}
}

func sagaStatusStyle(p palette, status schema.SagaStatus) lipgloss.Style {
	switch status {
	case schema.SagaSucceeded:
		return p.good
	case schema.SagaFailed, schema.SagaCanceled:
		return p.bad
	default:
		return p.warn
	}
}

func dependencyLine(p palette, label string, dep client.DependencyStatus) string {
	status := firstNonEmpty(dep.Status, "unknown")
	return fmt.Sprintf("%-13s %s  %s", label, p.command.Render(status), p.subtle.Render(firstNonEmpty(dep.ProviderID, dep.Summary)))
}

func renderMarkdown(markdown string, width int, noColor bool) (string, error) {
	if noColor {
		return markdown, nil
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	return renderer.Render(markdown)
}

func trimTime(value string) string {
	if len(value) >= 19 {
		return value[11:19]
	}
	return firstNonEmpty(value, "--:--:--")
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
