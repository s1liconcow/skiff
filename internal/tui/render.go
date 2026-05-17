package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const minRenderWidth = 64

type palette struct {
	shell      lipgloss.Style
	header     lipgloss.Style
	brand      lipgloss.Style
	title      lipgloss.Style
	meta       lipgloss.Style
	stat       lipgloss.Style
	statLabel  lipgloss.Style
	statValue  lipgloss.Style
	panel      lipgloss.Style
	focus      lipgloss.Style
	row        lipgloss.Style
	selected   lipgloss.Style
	good       lipgloss.Style
	warn       lipgloss.Style
	bad        lipgloss.Style
	pill       lipgloss.Style
	mutating   lipgloss.Style
	command    lipgloss.Style
	commandBox lipgloss.Style
	disabled   lipgloss.Style
	highlight  lipgloss.Style
	tableHead  lipgloss.Style
	key        lipgloss.Style
	footer     lipgloss.Style
}

func render(m Model) string {
	p := styles(m.noColor)
	width := m.width
	if width <= 0 {
		width = 118
	}
	if width < minRenderWidth {
		width = minRenderWidth
	}
	if m.err != nil {
		return p.shell.Width(width).Render(p.panel.Width(width).Render("TUI error: "+m.err.Error())) + "\n"
	}

	header := renderHeader(m, p, width)
	stats := renderStats(m, p, width)
	body := renderBody(m, p, width)
	footer := renderFooter(m, p, width)
	return p.shell.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, header, stats, body, footer)) + "\n"
}

func renderBody(m Model, p palette, width int) string {
	if width < 104 {
		return lipgloss.JoinVertical(lipgloss.Left,
			renderServices(m, p, width),
			renderSagas(m, p, width),
			renderServiceDetail(m, p, width),
			renderEvents(m, p, width),
			renderCommandPalette(m, p, width),
		)
	}
	gap := 2
	leftW := clamp(width*38/100, 36, 52)
	rightW := width - leftW - gap
	left := lipgloss.JoinVertical(lipgloss.Left,
		renderServices(m, p, leftW),
		renderSagas(m, p, leftW),
	)
	right := lipgloss.JoinVertical(lipgloss.Left,
		renderServiceDetail(m, p, rightW),
		renderEvents(m, p, rightW),
		renderCommandPalette(m, p, rightW),
	)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
}

func renderHeader(m Model, p palette, width int) string {
	status := "ready"
	if m.loading {
		status = strings.TrimSpace(m.spinner.View()) + " refreshing"
	}
	left := p.brand.Render("Skiff") + " " + p.title.Render("Operations")
	top := joinSpaced(left, p.pill.Render(status), max(1, width-4))

	meta := []string{
		"env " + firstNonEmpty(m.dashboard.Status.Env, "unknown"),
		"mode " + firstNonEmpty(string(m.dashboard.Status.Mode), "unknown"),
		"source " + firstNonEmpty(m.dashboard.Source, m.dashboard.Freshness.Source, "unknown"),
		fmt.Sprintf("fresh %ds", m.dashboard.Freshness.FreshnessSeconds),
	}
	if provider := providerSummary(m); provider != "" {
		meta = append(meta, provider)
	}
	if m.trace() != "" {
		meta = append(meta, "trace "+m.trace())
	}
	if m.readOnly {
		meta = append(meta, "read-only")
	}

	metaLine := fit(strings.Join(meta, "  /  "), max(1, width-4))
	return p.header.Width(width).Render(top + "\n" + p.meta.Render(metaLine))
}

func renderStats(m Model, p palette, width int) string {
	healthy, watch, failed := serviceHealthCounts(m.dashboard.Status.Services)
	findings := findingsCount(m)
	chips := []statChip{
		{Label: "Services", Value: fmt.Sprintf("%d", len(m.dashboard.Status.Services)), Detail: fmt.Sprintf("%d ok / %d watch / %d fail", healthy, watch, failed)},
		{Label: "Active Sagas", Value: fmt.Sprintf("%d", len(m.dashboard.Sagas)), Detail: sagaStatusSummary(m.dashboard.Sagas)},
		{Label: "Recent Events", Value: fmt.Sprintf("%d", len(m.dashboard.Events)), Detail: firstNonEmpty(m.dashboard.Source, m.dashboard.Freshness.Source, "object state")},
		{Label: "Findings", Value: fmt.Sprintf("%d", findings), Detail: findingsSummary(findings)},
	}

	gap := 1
	columns := 4
	if width < 112 {
		columns = 2
	}
	if width < 76 {
		columns = 1
	}
	chipW := (width - ((columns - 1) * gap)) / columns
	var rows []string
	for i := 0; i < len(chips); i += columns {
		var row []string
		for j := 0; j < columns && i+j < len(chips); j++ {
			chip := chips[i+j]
			row = append(row, renderStatChip(p, chip.Label, chip.Value, chip.Detail, chipW))
		}
		rows = append(rows, strings.Join(row, strings.Repeat(" ", gap)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func renderServices(m Model, p palette, width int) string {
	lines := []string{panelTitle(p, "Services", len(m.dashboard.Status.Services))}
	inner := innerWidth(width)
	if len(m.dashboard.Status.Services) == 0 {
		lines = append(lines, p.meta.Render("No services in object state"))
		return panelForFocus(m, p, focusServices).Width(width).Render(strings.Join(lines, "\n"))
	}
	lines = append(lines, p.tableHead.Render(serviceHeader(inner)))
	limit := listLimit(m.height, 8)
	for i, service := range m.dashboard.Status.Services {
		if i >= limit {
			lines = append(lines, p.meta.Render(fmt.Sprintf("... %d more services", len(m.dashboard.Status.Services)-limit)))
			break
		}
		prefix := "  "
		style := p.row
		if m.focus == focusServices && i == m.selected {
			prefix = "> "
			style = p.selected
		}
		line := renderServiceRow(p, prefix, service, inner)
		lines = append(lines, style.Width(inner).Render(line))
	}
	return panelForFocus(m, p, focusServices).Width(width).Render(strings.Join(lines, "\n"))
}

func renderSagas(m Model, p palette, width int) string {
	lines := []string{panelTitle(p, "Sagas", len(m.dashboard.Sagas))}
	inner := innerWidth(width)
	if len(m.dashboard.Sagas) == 0 {
		lines = append(lines, p.meta.Render("No active saga controls"))
		return panelForFocus(m, p, focusSagas).Width(width).Render(strings.Join(lines, "\n"))
	}
	lines = append(lines, p.tableHead.Render(sagaHeader(inner)))
	limit := listLimit(m.height, 7)
	for i, saga := range m.dashboard.Sagas {
		if i >= limit {
			lines = append(lines, p.meta.Render(fmt.Sprintf("... %d more sagas", len(m.dashboard.Sagas)-limit)))
			break
		}
		prefix := "  "
		style := p.row
		if m.focus == focusSagas && i == m.selected {
			prefix = "> "
			style = p.selected
		}
		line := renderSagaRow(p, prefix, saga, inner)
		lines = append(lines, style.Width(inner).Render(line))
	}
	return panelForFocus(m, p, focusSagas).Width(width).Render(strings.Join(lines, "\n"))
}

func renderServiceDetail(m Model, p palette, width int) string {
	service := m.selectedService()
	lines := []string{panelTitle(p, "Selected Service", 0)}
	if service.Service == "" {
		lines = append(lines, p.meta.Render("Select a service to inspect rollout, provider resources, logs, metrics, and recovery actions."))
		return p.panel.Width(width).Render(strings.Join(lines, "\n"))
	}

	lines = append(lines,
		p.title.Render(service.Service)+"  "+healthStyle(p, service.Health).Render(firstNonEmpty(service.Health, "unknown")),
		"desired "+p.command.Render(firstNonEmpty(service.DesiredRelease, "<none>"))+"  stable "+p.command.Render(firstNonEmpty(service.StableRelease, "<none>")),
	)
	if service.OperationID != "" {
		lines = append(lines, "operation "+p.command.Render(service.OperationID)+"  "+p.meta.Render(firstNonEmpty(service.OperationKind, "operation")+" / "+firstNonEmpty(service.OperationState, "running")))
	}
	if service.Rollout != nil {
		lines = append(lines, "rollout "+p.warn.Render(firstNonEmpty(service.Rollout.Status, "unknown"))+"  provider "+p.command.Render(firstNonEmpty(service.Rollout.ProviderID, "<none>")))
		if service.Rollout.Summary != "" {
			lines = append(lines, p.meta.Render(service.Rollout.Summary))
		}
	}

	lines = append(lines,
		"",
		p.highlight.Render("Cloud primitives"),
		dependencyLine(p, "capacity/asg", service.Capacity),
		dependencyLine(p, "target group", service.TargetHealth),
		dependencyLine(p, "logs", service.Logs),
		dependencyLine(p, "metrics", service.Metrics),
	)
	if len(service.Resources) > 0 {
		for _, resource := range service.Resources {
			lines = append(lines, "resource "+p.command.Render(firstNonEmpty(resource.Kind, resource.LogicalKind))+"  "+p.meta.Render(firstNonEmpty(resource.ProviderID, resource.LogicalName)))
		}
	}
	if len(service.Findings) > 0 {
		lines = append(lines, "", p.warn.Render("Findings"))
		for _, finding := range service.Findings {
			lines = append(lines, "  "+fit(finding.Code, 26)+"  "+finding.Summary)
		}
	}
	return p.panel.Width(width).Render(strings.Join(lines, "\n"))
}

func renderEvents(m Model, p palette, width int) string {
	lines := []string{panelTitle(p, "Recent Events", len(m.dashboard.Events))}
	inner := innerWidth(width)
	if len(m.dashboard.Events) == 0 {
		lines = append(lines, p.meta.Render("No recent events"))
		return panelForFocus(m, p, focusEvents).Width(width).Render(strings.Join(lines, "\n"))
	}
	limit := listLimit(m.height, 8)
	for i, event := range m.dashboard.Events {
		if i >= limit {
			lines = append(lines, p.meta.Render(fmt.Sprintf("... %d more events", len(m.dashboard.Events)-limit)))
			break
		}
		style := p.row
		prefix := "  "
		if m.focus == focusEvents && i == m.selected {
			style = p.selected
			prefix = "> "
		}
		subject := event.Subject.Kind + ":" + event.Subject.Name
		available := max(1, inner-lipgloss.Width(prefix))
		line := prefix + column(trimTime(event.Time), 8) + " " + column(event.Type, 22) + " " + fit(subject, max(12, available-33))
		lines = append(lines, style.Width(inner).Render(line))
	}
	return panelForFocus(m, p, focusEvents).Width(width).Render(strings.Join(lines, "\n"))
}

func renderCommandPalette(m Model, p palette, width int) string {
	title := panelTitle(p, "Command Palette", 0)
	if m.action == nil {
		lines := []string{
			title,
			renderActionHints(p, []ActionHint{
				{Key: "d", Label: "doctor"}, {Key: "l", Label: "logs"}, {Key: "m", Label: "metrics"}, {Key: "e", Label: "events"},
				{Key: "x", Label: "saga"}, {Key: "b", Label: "rollback"}, {Key: "a", Label: "approve"},
			}, innerWidth(width)),
		}
		if m.readOnly {
			lines = append(lines, p.meta.Render("Mutating actions are blocked by read-only mode."))
		} else {
			lines = append(lines, p.meta.Render("Mutating actions still emit explicit, auditable Skiff commands."))
		}
		return p.panel.Width(width).Render(strings.Join(lines, "\n"))
	}

	action := *m.action
	state := "read action"
	style := p.good
	if action.Mutating {
		state = fmt.Sprintf("mutating / risk %s / %s", firstNonEmpty(string(action.Risk), "medium"), firstNonEmpty(string(action.Reversibility), "compensatable"))
		style = p.mutating
	}
	if !action.Allowed {
		state = "blocked by read-only mode"
		style = p.disabled
	}

	md := fmt.Sprintf("**%s** - %s\n\n%s", action.Label, state, firstNonEmpty(action.Summary, "Ready"))
	lines := []string{title}
	if rendered, err := renderMarkdown(md, innerWidth(width), m.noColor); err == nil && strings.TrimSpace(rendered) != "" {
		lines = append(lines, strings.TrimSpace(rendered))
	} else {
		lines = append(lines, style.Render(action.Label+"  "+state))
	}
	lines = append(lines, p.commandBox.Width(innerWidth(width)).Render(wrapWords(action.Command, max(12, innerWidth(width)-4))))
	return p.panel.Width(width).Render(strings.Join(lines, "\n"))
}

func renderFooter(m Model, p palette, width int) string {
	h := m.help
	h.Width = width
	view := h.View(m.keys)
	if strings.TrimSpace(view) == "" {
		return ""
	}
	return p.footer.Width(width).Render(view)
}

func styles(noColor bool) palette {
	if noColor {
		base := lipgloss.NewStyle()
		panel := base.Border(lipgloss.NormalBorder()).Padding(0, 1)
		focus := base.Border(lipgloss.DoubleBorder()).Padding(0, 1)
		return palette{
			shell:      base,
			header:     panel,
			brand:      base,
			title:      base,
			meta:       base,
			stat:       panel,
			statLabel:  base,
			statValue:  base,
			panel:      panel,
			focus:      focus,
			row:        base,
			selected:   base,
			good:       base,
			warn:       base,
			bad:        base,
			pill:       base,
			mutating:   base,
			command:    base,
			commandBox: base.Border(lipgloss.NormalBorder()).Padding(0, 1),
			disabled:   base,
			highlight:  base,
			tableHead:  base,
			key:        base,
			footer:     base,
		}
	}
	return palette{
		shell:      lipgloss.NewStyle(),
		header:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("45")).Padding(0, 1),
		brand:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("45")).Padding(0, 1),
		title:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")),
		meta:       lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		stat:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1),
		statLabel:  lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		statValue:  lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true),
		panel:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1),
		focus:      lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(lipgloss.Color("220")).Padding(0, 1),
		row:        lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		selected:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("30")),
		good:       lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		warn:       lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
		bad:        lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		pill:       lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("220")).Bold(true).Padding(0, 1),
		mutating:   lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
		command:    lipgloss.NewStyle().Foreground(lipgloss.Color("81")),
		commandBox: lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("45")).Foreground(lipgloss.Color("81")).Padding(0, 1),
		disabled:   lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Strikethrough(true),
		highlight:  lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true),
		tableHead:  lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		key:        lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true),
		footer:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")).PaddingTop(1),
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
	target := firstNonEmpty(dep.ProviderID, dep.Summary, dep.Source)
	return column(label, 13) + " " + p.command.Render(column(status, 12)) + " " + p.meta.Render(firstNonEmpty(target, "<not observed>"))
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

func panelTitle(p palette, title string, count int) string {
	if count > 0 {
		return p.highlight.Render(title) + " " + p.meta.Render(fmt.Sprintf("%d", count))
	}
	return p.highlight.Render(title)
}

func serviceHeader(width int) string {
	if width < 54 {
		return column("service", max(10, width-16)) + " health"
	}
	nameW, releaseW, opW := serviceColumns(width)
	return column("service", nameW) + " " + column("health", 10) + " " + column("release", releaseW) + " " + column("operation", opW)
}

func renderServiceRow(p palette, prefix string, service client.ServiceStatus, width int) string {
	available := max(1, width-lipgloss.Width(prefix))
	if available < 54 {
		nameW := max(10, available-16)
		return prefix + column(service.Service, nameW) + " " + healthStyle(p, service.Health).Render(firstNonEmpty(service.Health, "unknown"))
	}
	nameW, releaseW, opW := serviceColumns(available)
	op := ""
	if service.OperationID != "" {
		op = service.OperationID
		if service.OperationState != "" {
			op += ":" + service.OperationState
		}
	}
	return prefix +
		column(service.Service, nameW) + " " +
		healthStyle(p, service.Health).Render(column(firstNonEmpty(service.Health, "unknown"), 10)) + " " +
		column(firstNonEmpty(service.DesiredRelease, "<none>"), releaseW) + " " +
		column(firstNonEmpty(op, "-"), opW)
}

func serviceColumns(width int) (int, int, int) {
	nameW := clamp(width-39, 12, 26)
	releaseW := clamp((width-nameW)/3, 10, 18)
	opW := max(8, width-nameW-releaseW-13)
	return nameW, releaseW, opW
}

func sagaHeader(width int) string {
	idW, statusW, stepW := sagaColumns(width)
	return column("saga", idW) + " " + column("status", statusW) + " " + column("step", stepW)
}

func renderSagaRow(p palette, prefix string, saga client.SagaSummary, width int) string {
	available := max(1, width-lipgloss.Width(prefix))
	idW, statusW, stepW := sagaColumns(available)
	step := ""
	if len(saga.CurrentSteps) > 0 {
		step = strings.Join(saga.CurrentSteps, ",")
	}
	return prefix +
		column(saga.SagaID, idW) + " " +
		sagaStatusStyle(p, saga.Status).Render(column(string(saga.Status), statusW)) + " " +
		column(firstNonEmpty(step, "-"), stepW)
}

func sagaColumns(width int) (int, int, int) {
	statusW := 13
	idW := clamp(width-31, 12, 28)
	stepW := max(8, width-idW-statusW-4)
	return idW, statusW, stepW
}

type ActionHint struct {
	Key   string
	Label string
}

type statChip struct {
	Label  string
	Value  string
	Detail string
}

func renderActionHints(p palette, hints []ActionHint, width int) string {
	var lines []string
	current := ""
	for _, hint := range hints {
		part := p.key.Render(hint.Key) + " " + hint.Label
		if current == "" {
			current = part
			continue
		}
		next := current + "   " + part
		if width > 0 && lipgloss.Width(next) > width {
			lines = append(lines, current)
			current = part
			continue
		}
		current = next
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

func renderStatChip(p palette, label, value, detail string, width int) string {
	if width > 0 {
		detail = fit(detail, max(1, width-8))
	}
	body := p.statLabel.Render(label) + "\n" + p.statValue.Render(value) + " " + p.meta.Render(detail)
	if width > 0 {
		return p.stat.Width(width).Render(body)
	}
	return body
}

func serviceHealthCounts(services []client.ServiceStatus) (healthy, watch, failed int) {
	for _, service := range services {
		switch service.Health {
		case "healthy", "serving", "ok":
			healthy++
		case "failed", "critical", "unhealthy":
			failed++
		default:
			watch++
		}
	}
	return healthy, watch, failed
}

func sagaStatusSummary(sagas []client.SagaSummary) string {
	if len(sagas) == 0 {
		return "none running"
	}
	running := 0
	for _, saga := range sagas {
		if saga.Status == schema.SagaRunning || saga.Status == schema.SagaCompensating || saga.Status == schema.SagaPending {
			running++
		}
	}
	return fmt.Sprintf("%d running", running)
}

func findingsCount(m Model) int {
	count := len(m.dashboard.Status.Findings)
	for _, service := range m.dashboard.Status.Services {
		count += len(service.Findings)
	}
	return count
}

func findingsSummary(count int) string {
	if count == 0 {
		return "clean"
	}
	return "needs attention"
}

func providerSummary(m Model) string {
	provider := strings.TrimSpace(m.dashboard.Status.Provider)
	region := strings.TrimSpace(m.dashboard.Status.Region)
	switch {
	case provider != "" && region != "":
		return "provider " + provider + "/" + region
	case provider != "":
		return "provider " + provider
	case region != "":
		return "region " + region
	default:
		return ""
	}
}

func listLimit(height, fallback int) int {
	if height <= 0 {
		return fallback
	}
	return clamp((height-14)/3, 4, fallback)
}

func innerWidth(width int) int {
	return max(1, width-4)
}

func joinSpaced(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func column(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = fit(value, width)
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func fit(value string, width int) string {
	value = strings.TrimSpace(value)
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return string([]rune(value)[:min(width, len([]rune(value)))])
	}
	runes := []rune(value)
	if len(runes) <= width-3 {
		return value
	}
	return string(runes[:width-3]) + "..."
}

func wrapWords(value string, width int) string {
	value = strings.TrimSpace(value)
	if value == "" || width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	words := strings.Fields(value)
	var lines []string
	current := ""
	for _, word := range words {
		if current == "" {
			current = word
			continue
		}
		next := current + " " + word
		if lipgloss.Width(next) > width {
			lines = append(lines, current)
			current = word
			continue
		}
		current = next
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
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
