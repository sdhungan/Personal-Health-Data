package views

import (
	"fmt"
	"strings"
)

// renderBarChartSVG renders c as a small inline bar chart with an
// optional goal line — server-rendered so no client-side charting
// library is needed, consistent with the rest of this project's
// "backend renders, browser displays" approach. id must be unique among
// charts rendered on the page at once (see chartInstanceID) since it
// namespaces the Datastar signals driving this chart's hover
// crosshair/tooltip — purely client-side, no server round-trip per hover.
func renderBarChartSVG(c ChartData, id string) string {
	const (
		width      = 340
		height     = 160
		leftAxis   = 34
		bottomAxis = 20
		topPad     = 10
	)
	plotW := float64(width - leftAxis - 10)
	plotH := float64(height - bottomAxis - topPad)

	maxVal, minVal := 0.0, 0.0
	for _, v := range c.Values {
		if v > maxVal {
			maxVal = v
		}
		if v < minVal {
			minVal = v
		}
	}
	if c.Goal != nil && *c.Goal > maxVal {
		maxVal = *c.Goal
	}
	if maxVal <= 0 {
		maxVal = 1
	}
	// Round the axis ceiling up to a "nice" number so the top gridline
	// reads cleanly (matches the reference dashboard's "40000" style).
	// axisMin stays 0 for the common case (steps, calories, etc. never go
	// negative) — only series like caloric deficit/surplus dip below zero,
	// so the floor is rounded the same "nice" way, and the plotted range
	// becomes [axisMin, axisMax] instead of always [0, axisMax].
	axisMax := niceCeiling(maxVal)
	axisMin := 0.0
	if minVal < 0 {
		axisMin = -niceCeiling(-minVal)
	}
	axisRange := axisMax - axisMin

	n := len(c.Values)
	if n == 0 {
		return ""
	}
	barGap := plotW / float64(n) * 0.25
	barW := plotW/float64(n) - barGap

	valueToY := func(v float64) float64 { return topPad + plotH*(axisMax-v)/axisRange }
	zeroY := valueToY(0)

	var b strings.Builder
	fmt.Fprintf(&b, `<div class="chart-wrap" data-signals="%s">`, escapeAttr(chartWrapSignals(id)))
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="chart-svg" preserveAspectRatio="xMidYMid meet">`, width, height)

	// Axis lines + faint intermediate gridlines (standardized: every chart
	// gets the same tick density) so values can be eyeballed without
	// needing to hover.
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`,
		leftAxis, int(topPad), leftAxis, height-bottomAxis)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`,
		leftAxis, height-bottomAxis, width-5, height-bottomAxis)
	for _, frac := range []float64{0.25, 0.5, 0.75} {
		v := axisMin + axisRange*frac
		gy := valueToY(v)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="chart-gridline"/>`, leftAxis, gy, width-5, gy)
		fmt.Fprintf(&b, `<text x="0" y="%.1f" class="chart-axis-label">%s</text>`, gy+3, formatAxisNumber(v))
	}
	fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-axis-label">%s</text>`, int(topPad)+6, formatAxisNumber(axisMax))
	fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-axis-label">%s</text>`, height-bottomAxis+4, formatAxisNumber(axisMin))

	// Explicit zero baseline once the range dips below zero — bars anchor
	// here instead of the bottom edge, so it needs to read clearly as "0",
	// distinct from the plot's own bottom border.
	if axisMin < 0 {
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="chart-axis"/>`, leftAxis, zeroY, width-5, zeroY)
	}

	// Goal line.
	if c.Goal != nil {
		goalY := valueToY(*c.Goal)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="chart-goal-line"/>`,
			leftAxis, goalY, width-5, goalY)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" class="chart-goal-label">Goal: %s</text>`,
			width-140, goalY-6, formatAxisNumber(*c.Goal))
	}

	// Bars, each anchored at the zero baseline and extending up (positive
	// values) or down (negative values) from it, and each a discrete hover
	// target — snapping the crosshair/tooltip straight to the exact value
	// is more precise for "read the exact measurement" than a
	// free-floating continuous crosshair would be.
	for i, v := range c.Values {
		x := float64(leftAxis) + float64(i)*(barW+barGap) + barGap/2
		top := valueToY(max(v, 0))
		bottom := valueToY(min(v, 0))
		barH := bottom - top
		y := top
		label := ""
		if i < len(c.Labels) {
			label = c.Labels[i]
		}
		xpct := (x + barW/2) / width * 100
		ypct := y / height * 100
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" class="chart-bar" %s/>`,
			x, y, barW, barH, hoverAttrs(id, xpct, ypct, label, formatAxisNumber(v)))
		if label != "" {
			fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="chart-bar-label">%s</text>`,
				x+barW/2, height-4, escapeSVGText(label))
		}
	}

	b.WriteString(`</svg>`)
	b.WriteString(renderChartOverlay(id))
	b.WriteString(`</div>`)
	return b.String()
}

// renderLineChartSVG renders a value-over-time line (heart rate, intraday
// or per-activity) with the same hover crosshair/tooltip and gridline
// treatment as renderBarChartSVG.
func renderLineChartSVG(samples []HRSample, id string) string {
	const (
		width      = 480
		height     = 140
		leftAxis   = 34
		bottomAxis = 18
		topPad     = 10
	)
	if len(samples) == 0 {
		return ""
	}
	plotW := float64(width - leftAxis - 10)
	plotH := float64(height - bottomAxis - topPad)

	minV, maxV := samples[0].Bpm, samples[0].Bpm
	for _, s := range samples {
		if s.Bpm < minV {
			minV = s.Bpm
		}
		if s.Bpm > maxV {
			maxV = s.Bpm
		}
	}
	if maxV == minV {
		maxV = minV + 1
	}
	pad := (maxV - minV) * 0.1
	minV -= pad
	maxV += pad

	n := len(samples)
	xAt := func(i int) float64 { return float64(leftAxis) + plotW*float64(i)/float64(max(n-1, 1)) }
	yAt := func(bpm float64) float64 { return topPad + plotH*(1-(bpm-minV)/(maxV-minV)) }

	var b strings.Builder
	fmt.Fprintf(&b, `<div class="chart-wrap" data-signals="%s">`, escapeAttr(chartWrapSignals(id)))
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="chart-svg" preserveAspectRatio="xMidYMid meet">`, width, height)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`, leftAxis, int(topPad), leftAxis, height-bottomAxis)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`, leftAxis, height-bottomAxis, width-5, height-bottomAxis)
	for _, frac := range []float64{0.25, 0.5, 0.75} {
		v := minV + (maxV-minV)*frac
		gy := yAt(v)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="chart-gridline"/>`, leftAxis, gy, width-5, gy)
		fmt.Fprintf(&b, `<text x="0" y="%.1f" class="chart-axis-label">%s</text>`, gy+3, formatAxisNumber(v))
	}
	fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-axis-label">%s</text>`, int(topPad)+6, formatAxisNumber(maxV))
	fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-axis-label">%s</text>`, height-bottomAxis+4, formatAxisNumber(minV))

	// Standardized time-axis ticks: up to 5 evenly spaced labels rather
	// than one per sample (which for a few hundred intraday points would
	// be illegible), matching the hourly steps chart's every-Nth-label
	// treatment.
	tickEvery := max(n/5, 1)
	for i := 0; i < n; i += tickEvery {
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="chart-bar-label">%s</text>`,
			xAt(i), height-2, escapeSVGText(samples[i].TimeLabel))
	}

	var points strings.Builder
	for i, s := range samples {
		if i > 0 {
			points.WriteByte(' ')
		}
		fmt.Fprintf(&points, "%.1f,%.1f", xAt(i), yAt(s.Bpm))
	}
	fmt.Fprintf(&b, `<polyline points="%s" class="chart-line"/>`, points.String())

	// One larger, invisible hover target per sample (easier to hit than
	// the 1-2px visual line) rather than tracking mouse position
	// continuously — same discrete snap-to-point precision as the bars.
	for i, s := range samples {
		x, y := xAt(i), yAt(s.Bpm)
		xpct, ypct := x/width*100, y/height*100
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="8" class="chart-hit-target" %s/>`,
			x, y, hoverAttrs(id, xpct, ypct, s.TimeLabel, formatAxisNumber(s.Bpm)+" bpm"))
	}

	b.WriteString(`</svg>`)
	b.WriteString(renderChartOverlay(id))
	b.WriteString(`</div>`)
	return b.String()
}

// hoverAttrs builds the data-on:mouseenter/mouseleave pair one bar/point
// needs to drive its chart's crosshair+tooltip signals. xpct/ypct are
// percentages of the SVG's own viewBox — since the SVG scales responsively
// (width:100%, preserveAspectRatio), positioning the HTML tooltip/crosshair
// overlay in the same percentage terms (see renderChartOverlay) keeps it
// aligned regardless of the chart's actual on-screen size, with no pixel
// math needed client-side.
func hoverAttrs(id string, xpct, ypct float64, xLabel, yValue string) string {
	set := fmt.Sprintf(
		`$tip_%s_show=true; $tip_%s_xpct=%.2f; $tip_%s_ypct=%.2f; $tip_%s_xlabel='%s'; $tip_%s_yvalue='%s'`,
		id, id, xpct, id, ypct, id, jsEscape(xLabel), id, jsEscape(yValue))
	clear := fmt.Sprintf(`$tip_%s_show=false`, id)
	return fmt.Sprintf(`data-on:mouseenter="%s" data-on:mouseleave="%s"`, escapeAttr(set), escapeAttr(clear))
}

// renderChartOverlay is the crosshair (vertical + horizontal line) and
// tooltip box shared by every hoverable chart, positioned purely via the
// $tip_<id>_* signals hoverAttrs sets — no server round-trip, no JS beyond
// what Datastar's own data-attr/data-text primitives provide.
//
// Visibility is folded into each data-attr:style expression as a leading
// "display:none;" rather than a separate data-show on the same element.
// Confirmed empirically (see prerequisite.md): data-show and data-attr:style
// on the SAME element both end up writing the style attribute, and in this
// Datastar version that combination causes an infinite reactive loop that
// hangs the page — not a hypothetical, this was reproduced in isolation
// and fixed by giving each element exactly one thing driving its style
// attribute.
//
// The signals are pre-declared with safe defaults on the wrapping element
// (rather than left to spring into existence on first hover) so these
// bindings never evaluate against an undefined signal on the initial
// render, before any mouseenter has fired.
func renderChartOverlay(id string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="chart-crosshair-v" data-attr:style="($tip_%s_show ? '' : 'display:none;') + 'left:' + $tip_%s_xpct + '%%'"></div>`, id, id)
	fmt.Fprintf(&b, `<div class="chart-crosshair-h" data-attr:style="($tip_%s_show ? '' : 'display:none;') + 'top:' + $tip_%s_ypct + '%%'"></div>`, id, id)
	fmt.Fprintf(&b, `<div class="chart-tooltip" data-attr:style="($tip_%s_show ? '' : 'display:none;') + 'left:' + $tip_%s_xpct + '%%; top:' + $tip_%s_ypct + '%%'">`, id, id, id)
	fmt.Fprintf(&b, `<div class="chart-tooltip-label" data-text="$tip_%s_xlabel"></div>`, id)
	fmt.Fprintf(&b, `<div class="chart-tooltip-value" data-text="$tip_%s_yvalue"></div>`, id)
	b.WriteString(`</div>`)
	return b.String()
}

// chartWrapSignals is the data-signals attribute text (without surrounding
// quotes) that pre-initializes one chart instance's hover state — see
// renderChartOverlay's doc comment. Uses the unquoted-key object-literal
// syntax from data-star.dev's own docs (data-signals="{foo: 1}"), not
// JSON.Marshal-style quoted keys — untested whether quoted keys would also
// work here, so this follows the documented form rather than assuming.
func chartWrapSignals(id string) string {
	return fmt.Sprintf(
		`{tip_%s_show: false, tip_%s_xpct: 0, tip_%s_ypct: 0, tip_%s_xlabel: '', tip_%s_yvalue: ''}`,
		id, id, id, id, id)
}

// chartInstanceID turns a DOM id or other identifier into a string safe to
// use both as a Datastar signal name segment and inside one — strips
// everything but letters/digits/underscore, since tile ids like
// "tile-resting_hr" or activity ids like "activity-42" otherwise carry
// hyphens JS identifiers can't have.
func chartInstanceID(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func jsEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return r.Replace(s)
}

// escapeAttr escapes text destined for an HTML attribute value (the
// mouseenter/mouseleave expressions themselves, already JS-quoted
// internally by jsEscape — this second pass is for the surrounding HTML
// double-quotes, e.g. the Format:'d numbers/labels never contain one but
// this stays correct if that ever changes).
func escapeAttr(s string) string {
	r := strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `<`, "&lt;", `>`, "&gt;")
	return r.Replace(s)
}

// renderSparklineSVG renders a tiny axis-less trend line — the collapsed-tile
// equivalent of the reference dashboard's small trend indicator, distinct
// from the full expanded chart. Not interactive: too small to hover
// meaningfully, and the expanded view right next to it already offers that.
func renderSparklineSVG(values []float64) string {
	const width, height = 100, 28
	if len(values) == 0 {
		return ""
	}
	minV, maxV := values[0], values[0]
	for _, v := range values {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV == minV {
		maxV = minV + 1
	}
	n := len(values)
	var points strings.Builder
	for i, v := range values {
		x := float64(i) / float64(max(n-1, 1)) * width
		y := height - (v-minV)/(maxV-minV)*height
		if i > 0 {
			points.WriteByte(' ')
		}
		fmt.Fprintf(&points, "%.1f,%.1f", x, y)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="sparkline-svg" preserveAspectRatio="none">`, width, height)
	fmt.Fprintf(&b, `<polyline points="%s" class="sparkline-line"/>`, points.String())
	b.WriteString(`</svg>`)
	return b.String()
}

// renderStageTimelineSVG renders a segmented horizontal bar — one rect per
// sleep stage, colored by stage type via CSS (see style.css's .stage-*
// classes) — a lightweight hypnogram, not a full sleep-chart library.
func renderStageTimelineSVG(stages []StageSegment, id string) string {
	const width, height = 480, 36
	if len(stages) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="chart-wrap" data-signals="%s">`, escapeAttr(chartWrapSignals(id)))
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="chart-svg stage-timeline" preserveAspectRatio="none">`, width, height)
	for _, s := range stages {
		start := clampPct(s.StartPct) * width
		end := clampPct(s.EndPct) * width
		w := end - start
		if w < 0 {
			w = 0
		}
		xpct := (start + w/2) / width * 100
		fmt.Fprintf(&b, `<rect x="%.1f" y="0" width="%.1f" height="%d" class="stage-seg stage-%s" %s/>`,
			start, w, height, strings.ToLower(s.Type), hoverAttrs(id, xpct, 50, s.TimeLabel, humanizeStageType(s.Type)))
	}
	b.WriteString(`</svg>`)
	b.WriteString(renderChartOverlay(id))
	b.WriteString(`</div>`)
	return b.String()
}

func niceCeiling(v float64) float64 {
	if v <= 0 {
		return 1
	}
	magnitude := 1.0
	for v/magnitude >= 10 {
		magnitude *= 10
	}
	for v/magnitude < 1 {
		magnitude /= 10
	}
	steps := []float64{1, 2, 2.5, 5, 10}
	scaled := v / magnitude
	for _, s := range steps {
		if scaled <= s {
			return s * magnitude
		}
	}
	return 10 * magnitude
}

func formatAxisNumber(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}

func escapeSVGText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
