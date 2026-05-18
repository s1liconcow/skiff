package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

const minRenderWidth = 64

type palette struct {
	shell      lipgloss.Style
	header     lipgloss.Style
	brand      lipgloss.Style
	brandMark  lipgloss.Style
	title      lipgloss.Style
	subtitle   lipgloss.Style
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
	section    lipgloss.Style
	tableHead  lipgloss.Style
	key        lipgloss.Style
	footer     lipgloss.Style
	okPill     lipgloss.Style
	warnPill   lipgloss.Style
	badPill    lipgloss.Style
	softBox    lipgloss.Style
	signal     lipgloss.Style
	divider    lipgloss.Style
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
		return p.shell.Width(width).Render(renderBox(p.panel, width, "TUI error: "+m.err.Error())) + "\n"
	}

	header := renderHeader(m, p, width)
	stats := renderStats(m, p, width)
	body := renderBody(m, p, width)
	footer := renderFooter(m, p, width)
	sections := []string{header, "", stats, "", body}
	if strings.TrimSpace(footer) != "" {
		sections = append(sections, footer)
	}
	return p.shell.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, sections...)) + "\n"
}

func renderBody(m Model, p palette, width int) string {
	if width < 104 {
		return lipgloss.JoinVertical(lipgloss.Left,
			renderServices(m, p, width),
			"",
			renderSagas(m, p, width),
			"",
			renderServiceDetail(m, p, width),
			"",
			renderEvents(m, p, width),
			"",
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
	inner := boxInnerWidth(p.header, width)
	status := "ready"
	if m.loading {
		status = strings.TrimSpace(m.spinner.View()) + " refreshing"
	}
	left := p.brandMark.Render("::") + " " + p.brand.Render("Skiff") + " " + p.title.Render("Operations Deck")
	top := joinSpaced(left, statusPill(p, status), inner)

	meta := []string{renderSignal(p, "env", firstNonEmpty(m.dashboard.Status.Env, "unknown"))}
	meta = append(meta,
		renderSignal(p, "mode", firstNonEmpty(string(m.dashboard.Status.Mode), "unknown")),
		renderSignal(p, "source", firstNonEmpty(m.dashboard.Source, m.dashboard.Freshness.Source, "unknown")),
		renderSignal(p, "fresh", fmt.Sprintf("%ds", m.dashboard.Freshness.FreshnessSeconds)),
	)
	if provider := providerSummary(m); provider != "" {
		meta = append(meta, renderSignal(p, "cloud", provider))
	}
	if m.trace() != "" {
		meta = append(meta, renderSignal(p, "trace", m.trace()))
	}
	if m.readOnly {
		meta = append(meta, p.warnPill.Render("read-only"))
	}

	tagline := p.subtitle.Render(fit("object storage truth / typed sagas / direct recovery", inner))
	metaLine := renderMetaChips(p, meta, inner)
	return renderBox(p.header, width, lipgloss.JoinVertical(lipgloss.Left, top, tagline, metaLine))
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
		rows = append(rows, joinBlocks(gap, row...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func renderServices(m Model, p palette, width int) string {
	lines := []string{panelTitle(p, "Services", len(m.dashboard.Status.Services))}
	inner := panelInnerWidth(p, width)
	if len(m.dashboard.Status.Services) == 0 {
		lines = append(lines, p.meta.Render("No services in object state"))
		return renderBox(panelForFocus(m, p, focusServices), width, strings.Join(lines, "\n"))
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
	return renderBox(panelForFocus(m, p, focusServices), width, strings.Join(lines, "\n"))
}

func renderSagas(m Model, p palette, width int) string {
	lines := []string{panelTitle(p, "Sagas", len(m.dashboard.Sagas))}
	inner := panelInnerWidth(p, width)
	if len(m.dashboard.Sagas) == 0 {
		lines = append(lines, p.meta.Render("No active saga controls"))
		return renderBox(panelForFocus(m, p, focusSagas), width, strings.Join(lines, "\n"))
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
	return renderBox(panelForFocus(m, p, focusSagas), width, strings.Join(lines, "\n"))
}

func renderServiceDetail(m Model, p palette, width int) string {
	service := m.selectedService()
	lines := []string{panelTitle(p, "Selected Service", 0)}
	inner := panelInnerWidth(p, width)
	if service.Service == "" {
		lines = append(lines, p.meta.Render("Select a service to inspect rollout, provider resources, logs, metrics, and recovery actions."))
		return renderBox(p.panel, width, strings.Join(lines, "\n"))
	}

	lines = append(lines,
		joinSpaced(p.title.Render(fit(service.Service, max(12, inner-18))), healthToken(p, service.Health), inner),
		renderReleaseRail(p, service, inner),
	)
	if service.OperationID != "" {
		plain := "operation " + service.OperationID + "  " + firstNonEmpty(service.OperationKind, "operation") + " / " + firstNonEmpty(service.OperationState, "running")
		if lipgloss.Width(plain) > inner {
			lines = append(lines, fit(plain, inner))
		} else {
			lines = append(lines, "operation "+p.command.Render(service.OperationID)+"  "+p.meta.Render(firstNonEmpty(service.OperationKind, "operation")+" / "+firstNonEmpty(service.OperationState, "running")))
		}
	}
	if service.Rollout != nil {
		plain := "rollout " + firstNonEmpty(service.Rollout.Status, "unknown") + "  provider " + firstNonEmpty(service.Rollout.ProviderID, "<none>")
		if lipgloss.Width(plain) > inner {
			lines = append(lines, fit(plain, inner))
		} else {
			lines = append(lines, "rollout "+p.warn.Render(firstNonEmpty(service.Rollout.Status, "unknown"))+"  provider "+p.command.Render(firstNonEmpty(service.Rollout.ProviderID, "<none>")))
		}
		if service.Rollout.Summary != "" {
			lines = append(lines, p.meta.Render(fit(service.Rollout.Summary, inner)))
		}
	}

	lines = append(lines,
		"",
		sectionTitle(p, "Cloud primitives"),
		dependencyLine(p, "capacity/asg", service.Capacity, inner),
		dependencyLine(p, "target group", service.TargetHealth, inner),
	)
	if service.Database.Status != "" {
		lines = append(lines, dependencyLine(p, "database", service.Database, inner))
	}
	lines = append(lines,
		dependencyLine(p, "logs", service.Logs, inner),
		dependencyLine(p, "metrics", service.Metrics, inner),
	)
	if len(service.Resources) > 0 {
		for _, resource := range service.Resources {
			lines = append(lines, resourceLine(p, resource, inner))
		}
	}
	if len(service.Findings) > 0 {
		lines = append(lines, "", sectionTitle(p, "Findings"))
		for _, finding := range service.Findings {
			code := fit(finding.Code, min(26, max(8, inner/3)))
			summaryW := max(8, inner-lipgloss.Width(code)-4)
			lines = append(lines, "  "+code+"  "+fit(finding.Summary, summaryW))
		}
	}
	return renderBox(p.panel, width, strings.Join(lines, "\n"))
}

func renderEvents(m Model, p palette, width int) string {
	lines := []string{panelTitle(p, "Recent Events", len(m.dashboard.Events))}
	inner := panelInnerWidth(p, width)
	if len(m.dashboard.Events) == 0 {
		lines = append(lines, p.meta.Render("No recent events"))
		return renderBox(panelForFocus(m, p, focusEvents), width, strings.Join(lines, "\n"))
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
		lines = append(lines, style.Width(inner).Render(renderEventRow(p, prefix, event, inner)))
	}
	return renderBox(panelForFocus(m, p, focusEvents), width, strings.Join(lines, "\n"))
}

func renderCommandPalette(m Model, p palette, width int) string {
	title := panelTitle(p, "Command Palette", 0)
	inner := panelInnerWidth(p, width)
	if m.action == nil {
		lines := []string{
			title,
			renderActionHints(p, []ActionHint{
				{Key: "d", Label: "doctor"}, {Key: "l", Label: "logs"}, {Key: "m", Label: "metrics"}, {Key: "e", Label: "events"},
				{Key: "x", Label: "saga"}, {Key: "b", Label: "rollback"}, {Key: "a", Label: "approve"},
			}, inner),
		}
		if m.readOnly {
			lines = append(lines, p.warn.Render("Mutating actions are blocked by read-only mode."))
		} else {
			lines = append(lines, p.meta.Render("Mutating actions still emit explicit, auditable Skiff commands."))
		}
		return renderBox(p.panel, width, strings.Join(lines, "\n"))
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
	if rendered, err := renderMarkdown(md, inner, m.noColor); err == nil && strings.TrimSpace(rendered) != "" {
		lines = append(lines, strings.TrimSpace(rendered))
	} else {
		lines = append(lines, style.Render(action.Label+"  "+state))
	}
	commandInner := boxInnerWidth(p.commandBox, inner)
	command := p.command.Render(wrapWords(action.Command, max(12, commandInner)))
	lines = append(lines, p.divider.Render(strings.Repeat("-", max(1, inner))))
	lines = append(lines, renderBox(p.commandBox, inner, command))
	return renderBox(p.panel, width, strings.Join(lines, "\n"))
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
			brandMark:  base,
			title:      base,
			subtitle:   base,
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
			section:    base,
			tableHead:  base,
			key:        base,
			footer:     base,
			okPill:     base,
			warnPill:   base,
			badPill:    base,
			softBox:    base,
			signal:     base,
			divider:    base,
		}
	}
	base := lipgloss.NewStyle()
	return palette{
		shell:      base.Foreground(lipgloss.Color("252")),
		header:     base.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("43")).Padding(0, 1),
		brand:      base.Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("43")).Padding(0, 1),
		brandMark:  base.Foreground(lipgloss.Color("43")).Bold(true),
		title:      base.Bold(true).Foreground(lipgloss.Color("231")),
		subtitle:   base.Foreground(lipgloss.Color("152")),
		meta:       base.Foreground(lipgloss.Color("245")),
		stat:       base.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1),
		statLabel:  base.Foreground(lipgloss.Color("246")),
		statValue:  base.Foreground(lipgloss.Color("231")).Bold(true),
		panel:      base.Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1),
		focus:      base.Border(lipgloss.ThickBorder()).BorderForeground(lipgloss.Color("220")).Padding(0, 1),
		row:        base.Foreground(lipgloss.Color("252")),
		selected:   base.Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("222")),
		good:       base.Foreground(lipgloss.Color("42")).Bold(true),
		warn:       base.Foreground(lipgloss.Color("214")).Bold(true),
		bad:        base.Foreground(lipgloss.Color("203")).Bold(true),
		pill:       base.Foreground(lipgloss.Color("16")).Background(lipgloss.Color("220")).Bold(true).Padding(0, 1),
		mutating:   base.Foreground(lipgloss.Color("214")).Bold(true),
		command:    base.Foreground(lipgloss.Color("81")),
		commandBox: base.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("43")).Foreground(lipgloss.Color("81")).Padding(0, 1),
		disabled:   base.Foreground(lipgloss.Color("240")).Strikethrough(true),
		highlight:  base.Foreground(lipgloss.Color("220")).Bold(true),
		section:    base.Foreground(lipgloss.Color("171")).Bold(true),
		tableHead:  base.Foreground(lipgloss.Color("244")),
		key:        base.Foreground(lipgloss.Color("16")).Background(lipgloss.Color("220")).Bold(true).Padding(0, 1),
		footer:     base.Foreground(lipgloss.Color("245")).PaddingTop(1),
		okPill:     base.Foreground(lipgloss.Color("16")).Background(lipgloss.Color("42")).Bold(true).Padding(0, 1),
		warnPill:   base.Foreground(lipgloss.Color("16")).Background(lipgloss.Color("220")).Bold(true).Padding(0, 1),
		badPill:    base.Foreground(lipgloss.Color("231")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1),
		softBox:    base.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1),
		signal:     base.Foreground(lipgloss.Color("81")),
		divider:    base.Foreground(lipgloss.Color("238")),
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

func healthToken(p palette, health string) string {
	value := firstNonEmpty(health, "unknown")
	switch health {
	case "healthy", "serving", "ok":
		return p.okPill.Render(value)
	case "failed", "critical", "unhealthy":
		return p.badPill.Render(value)
	default:
		return p.warnPill.Render(value)
	}
}

func healthCell(p palette, health string, width int) string {
	value := firstNonEmpty(health, "unknown")
	token := healthToken(p, value)
	if lipgloss.Width(token) <= width {
		return token + strings.Repeat(" ", width-lipgloss.Width(token))
	}
	return healthStyle(p, value).Render(column(value, width))
}

func statusPill(p palette, status string) string {
	status = firstNonEmpty(status, "ready")
	if strings.Contains(status, "refreshing") {
		return p.warnPill.Render(status)
	}
	return p.okPill.Render(status)
}

func renderSignal(p palette, label, value string) string {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(firstNonEmpty(value, "unknown"))
	if label == "" {
		return p.signal.Render(value)
	}
	return p.meta.Render(label+" ") + p.signal.Render(value)
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

func renderMetaChips(p palette, values []string, width int) string {
	if len(values) == 0 {
		return ""
	}
	var lines []string
	var current []string
	currentW := 0
	for _, value := range values {
		chip := p.softBox.Render(strings.TrimSpace(value))
		chipW := lipgloss.Width(chip)
		if len(current) == 0 {
			current = append(current, chip)
			currentW = chipW
			continue
		}
		nextW := currentW + 1 + chipW
		if width > 0 && nextW > width {
			lines = append(lines, joinBlocks(1, current...))
			current = []string{chip}
			currentW = chipW
			continue
		}
		current = append(current, chip)
		currentW = nextW
	}
	if len(current) > 0 {
		lines = append(lines, joinBlocks(1, current...))
	}
	return strings.Join(lines, "\n")
}

func renderReleaseRail(p palette, service client.ServiceStatus, width int) string {
	stable := firstNonEmpty(service.StableRelease, "<none>")
	desired := firstNonEmpty(service.DesiredRelease, "<none>")
	state := firstNonEmpty(service.OperationState, service.Health, "observing")
	plain := "stable " + stable + "  ->  desired " + desired + "  /  " + state
	body := p.meta.Render("stable ") + p.command.Render(stable) +
		p.meta.Render("  ->  desired ") + p.command.Render(desired) +
		p.meta.Render("  /  ") + healthStyle(p, state).Render(state)
	if width > 0 && lipgloss.Width(body) > width {
		return fit(plain, width)
	}
	return body
}

func renderEventRow(p palette, prefix string, event schema.Event, width int) string {
	available := max(1, width-lipgloss.Width(prefix))
	subject := firstNonEmpty(event.Subject.Kind+":"+event.Subject.Name, event.Subject.Name, event.Subject.Kind, "event")
	if available < 72 {
		return prefix + column(trimTime(event.Time), 8) + " " + column(event.Type, 22) + " " + fit(subject, max(12, available-33))
	}
	summaryW := max(10, available-66)
	subjectW := max(12, available-summaryW-35)
	return prefix +
		column(trimTime(event.Time), 8) + " " +
		p.command.Render(column(event.Type, 22)) + " " +
		column(subject, subjectW) + " " +
		p.meta.Render(fit(firstNonEmpty(event.Summary, "-"), summaryW))
}

func dependencyLine(p palette, label string, dep client.DependencyStatus, width int) string {
	status := firstNonEmpty(dep.Status, "unknown")
	target := firstNonEmpty(dep.ProviderID, dep.Summary, dep.Source)
	labelW := min(13, max(7, width/4))
	statusW := min(12, max(7, (width-labelW-2)/3))
	targetW := max(1, width-labelW-statusW-2)
	return column(label, labelW) + " " + p.command.Render(column(status, statusW)) + " " + p.meta.Render(fit(firstNonEmpty(target, "<not observed>"), targetW))
}

func resourceLine(p palette, resource servicestatus.ResourceSummary, width int) string {
	prefix := "resource "
	kind := firstNonEmpty(resource.Kind, resource.LogicalKind)
	target := firstNonEmpty(resource.ProviderID, resource.LogicalName)
	kindW := min(20, max(8, (width-lipgloss.Width(prefix))/3))
	targetW := max(1, width-lipgloss.Width(prefix)-kindW-1)
	return prefix + p.command.Render(column(kind, kindW)) + " " + p.meta.Render(fit(target, targetW))
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
		return p.highlight.Render(strings.ToUpper(title)) + " " + p.meta.Render(fmt.Sprintf("%d", count))
	}
	return p.highlight.Render(strings.ToUpper(title))
}

func sectionTitle(p palette, title string) string {
	return p.section.Render(strings.ToUpper(title))
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
		return prefix + column(service.Service, nameW) + " " + healthToken(p, service.Health)
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
		healthCell(p, service.Health, 10) + " " +
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
		sagaStatusCell(p, saga.Status, statusW) + " " +
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
		part := p.key.Render(hint.Key) + " " + p.meta.Render(hint.Label)
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
	inner := boxInnerWidth(p.stat, width)
	if width > 0 {
		detail = fit(detail, inner)
	}
	top := joinSpaced(p.statLabel.Render(strings.ToUpper(label)), p.statValue.Render(value), inner)
	body := top + "\n" + p.meta.Render(detail)
	if width > 0 {
		return renderBox(p.stat, width, body)
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

func sagaStatusToken(p palette, status schema.SagaStatus) string {
	switch status {
	case schema.SagaSucceeded:
		return p.okPill.Render(firstNonEmpty(string(status), "succeeded"))
	case schema.SagaFailed, schema.SagaCanceled:
		return p.badPill.Render(firstNonEmpty(string(status), "failed"))
	default:
		return p.warnPill.Render(firstNonEmpty(string(status), "pending"))
	}
}

func sagaStatusCell(p palette, status schema.SagaStatus, width int) string {
	value := firstNonEmpty(string(status), "pending")
	token := sagaStatusToken(p, status)
	if lipgloss.Width(token) <= width {
		return token + strings.Repeat(" ", width-lipgloss.Width(token))
	}
	return sagaStatusStyle(p, status).Render(column(value, width))
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

func panelInnerWidth(p palette, outerWidth int) int {
	return boxInnerWidth(p.panel, outerWidth)
}

func boxInnerWidth(style lipgloss.Style, outerWidth int) int {
	return max(1, outerWidth-style.GetHorizontalFrameSize())
}

func renderBox(style lipgloss.Style, outerWidth int, body string) string {
	return style.Width(boxInnerWidth(style, outerWidth)).Render(body)
}

func joinSpaced(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func joinBlocks(gap int, blocks ...string) string {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 {
		return blocks[0]
	}
	parts := make([]string, 0, len(blocks)*2-1)
	spacer := strings.Repeat(" ", max(1, gap))
	for i, block := range blocks {
		if i > 0 {
			parts = append(parts, spacer)
		}
		parts = append(parts, block)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
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
