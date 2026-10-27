package views

import (
	"fmt"
	"strings"
	"time"
)

// Bar and line charts share one layout: the SVG's viewBox is 0..100 on both
// axes with preserveAspectRatio="none", stretched over a .chart-plot box
// that fills whatever space its tile gives it (see style.css). A plot
// coordinate is therefore also a CSS percentage of that box, so the HTML
// axis labels and the hover overlay are positioned with the very same
// numbers — and since labels are HTML rather than SVG <text>, they stay
// crisp and unstretched at any tile size. Strokes use
// vector-effect: non-scaling-stroke for the same reason.

// chartGridlines draws the 3 faint interior gridlines (quarters of the range).
func chartGridlines(b *strings.Builder, axisMin, axisMax float64, y func(float64) float64) {
	for _, frac := range []float64{0.25, 0.5, 0.75} {
		gy := y(axisMin + (axisMax-axisMin)*frac)
		fmt.Fprintf(b, `<line x1="0" y1="%.2f" x2="100" y2="%.2f" class="chart-gridline"/>`, gy, gy)
	}
}

// chartYLabelSpans writes the 5 y-axis labels (min, quarters, max) as HTML.

func chartYLabelSpans(b *strings.Builder, axisMin, axisMax float64, y func(float64) float64) {
	for _, frac := range []float64{0, 0.25, 0.5, 0.75, 1} {
		v := axisMin + (axisMax-axisMin)*frac
		fmt.Fprintf(b, `<span class="chart-y-label" style="top:%.2f%%">%s</span>`, y(v), formatAxisNumber(v))
	}
}

// chartXLabelSpan writes one x-axis label. Every label is sent: the server
// can't know how wide a resizable tile will be, so static/chart-labels.js
// hides whichever ones would overlap at the real rendered width. ordinal is
// the label's position among the chart's labels; the first starts at its
// position instead of centering on it (x-first), so it can't reach left into
// the y-axis labels.
func chartXLabelSpan(b *strings.Builder, xpct float64, label string, ordinal int) {
	class := "chart-x-label"
	if ordinal == 0 {
		class += " x-first"
	}
	fmt.Fprintf(b, `<span class="%s" style="left:%.2f%%">%s</span>`, class, xpct, escapeSVGText(label))
}

func chartOpen(b *strings.Builder, id string) {
	fmt.Fprintf(b, `<div class="chart-wrap" data-signals="%s"><div class="chart-plot">`, escapeAttr(chartWrapSignals(id)))
	b.WriteString(`<svg viewBox="0 0 100 100" class="chart-svg" preserveAspectRatio="none">`)
}

func chartClose(b *strings.Builder, id string) {
	b.WriteString(renderChartOverlay(id))
	b.WriteString(`</div></div>`)
}

// renderBarChartSVG renders c as an inline bar chart with an optional goal
// line — server-rendered so no client-side charting library is needed,
// consistent with the rest of this project's "backend renders, browser
// displays" approach. id must be unique among charts rendered on the page
// at once (see chartInstanceID) since it namespaces the Datastar signals
// driving this chart's hover crosshair/tooltip — purely client-side, no
// server round-trip per hover.
func renderBarChartSVG(c ChartData, id string) string {
	n := len(c.Values)
	if n == 0 {
		return ""
	}
	maxVal, minVal := 0.0, 0.0
	for _, v := range c.Values {
		maxVal = max(maxVal, v)
		minVal = min(minVal, v)
	}
	if c.Goal != nil {
		maxVal = max(maxVal, *c.Goal)
	}
	if maxVal <= 0 {
		maxVal = 1
	}
	// Round the axis ceiling up to a "nice" number so the top gridline
	// reads cleanly. axisMin stays 0 for the common case (steps, calories,
	// etc. never go negative) — only series like caloric deficit/surplus
	// dip below zero, so the floor is rounded the same "nice" way.
	axisMax := niceCeiling(maxVal)
	axisMin := 0.0
	if minVal < 0 {
		axisMin = -niceCeiling(-minVal)
	}
	y := func(v float64) float64 { return 100 * (axisMax - v) / (axisMax - axisMin) }

	slot := 100 / float64(n)
	barW := slot * 0.75

	var b strings.Builder
	chartOpen(&b, id)
	chartGridlines(&b, axisMin, axisMax, y)
	// Explicit zero baseline once the range dips below zero — bars anchor
	// here instead of the bottom edge.
	if axisMin < 0 {
		fmt.Fprintf(&b, `<line x1="0" y1="%.2f" x2="100" y2="%.2f" class="chart-axis"/>`, y(0), y(0))
	}
	if c.Goal != nil {
		fmt.Fprintf(&b, `<line x1="0" y1="%.2f" x2="100" y2="%.2f" class="chart-goal-line"/>`, y(*c.Goal), y(*c.Goal))
	}
	// Each bar is a discrete hover target — snapping the crosshair/tooltip
	// straight to the exact value is more precise than a free-floating
	// continuous crosshair would be.
	for i, v := range c.Values {
		x := float64(i)*slot + (slot-barW)/2
		top, bottom := y(max(v, 0)), y(min(v, 0))
		label := ""
		if i < len(c.Labels) {
			label = c.Labels[i]
		}
		fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" class="chart-bar" %s/>`,
			x, top, barW, bottom-top, hoverAttrs(id, x+barW/2, top, label, formatAxisNumber(v)))
	}
	b.WriteString(`</svg>`)

	chartYLabelSpans(&b, axisMin, axisMax, y)
	k := 0
	for i, label := range c.Labels {
		if label != "" {
			chartXLabelSpan(&b, (float64(i)+0.5)*slot, label, k)
			k++
		}
	}
	if c.Goal != nil {
		fmt.Fprintf(&b, `<span class="chart-goal-label" style="top:%.2f%%">Goal: %s</span>`, y(*c.Goal), formatAxisNumber(*c.Goal))
	}
	chartClose(&b, id)
	return b.String()
}

// renderLineChartSVG renders a value-over-time line (heart rate, intraday
// or per-activity; resting HR/HRV over 14 days) with the same hover
// crosshair/tooltip and gridline treatment as renderBarChartSVG. unit
// suffixes the hover tooltip value.
// maxDotSamples is the most samples a line chart draws individual dots for.
const maxDotSamples = 31

func renderLineChartSVG(samples []HRSample, id, unit string) string {
	n := len(samples)
	if n == 0 {
		return ""
	}
	minV, maxV := samples[0].Bpm, samples[0].Bpm
	for _, s := range samples {
		minV = min(minV, s.Bpm)
		maxV = max(maxV, s.Bpm)
	}
	// Dynamic y-axis: data min-10 to max+10, never anchored at 0, so a
	// day-to-day trend (resting HR, HRV) stays visible.
	minV -= 10
	maxV += 10

	x := func(i int) float64 {
		if n == 1 {
			return 50
		}
		return 100 * float64(i) / float64(n-1)
	}
	y := func(v float64) float64 { return 100 * (maxV - v) / (maxV - minV) }

	var b strings.Builder
	chartOpen(&b, id)
	chartGridlines(&b, minV, maxV, y)

	var points strings.Builder
	for i, s := range samples {
		if i > 0 {
			points.WriteByte(' ')
		}
		fmt.Fprintf(&points, "%.2f,%.2f", x(i), y(s.Bpm))
	}
	fmt.Fprintf(&b, `<polyline points="%s" class="chart-line"/>`, points.String())

	// One full-height invisible column per sample, reaching halfway to its
	// neighbours, as the hover target — easier to hit than the thin line,
	// and unlike a circle it doesn't distort when the plot stretches.
	for i, s := range samples {
		left, right := 0.0, 100.0
		if i > 0 {
			left = (x(i-1) + x(i)) / 2
		}
		if i < n-1 {
			right = (x(i) + x(i+1)) / 2
		}
		fmt.Fprintf(&b, `<rect x="%.2f" y="0" width="%.2f" height="100" class="chart-hit-target" %s/>`,
			left, right-left, hoverAttrs(id, x(i), y(s.Bpm), s.TimeLabel, formatAxisNumber(s.Bpm)+" "+unit))
	}
	b.WriteString(`</svg>`)

	// A visible dot per sample when there are few enough to tell apart
	// (14-day resting HR/HRV), not on dense intraday heart rate. HTML rather
	// than SVG circles so they stay round on the stretched plot.
	if n <= maxDotSamples {
		for i, s := range samples {
			fmt.Fprintf(&b, `<span class="chart-dot" style="left:%.2f%%;top:%.2f%%"></span>`, x(i), y(s.Bpm))
		}
	}

	chartYLabelSpans(&b, minV, maxV, y)
	// Up to 5 evenly spaced time labels rather than one per sample (which
	// for a few hundred intraday points would be illegible).
	for i, k := 0, 0; i < n; i, k = i+max(n/5, 1), k+1 {
		chartXLabelSpan(&b, x(i), samples[i].TimeLabel, k)
	}
	chartClose(&b, id)
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
	fmt.Fprintf(&b, `<div class="chart-wrap stage-wrap" data-signals="%s">`, escapeAttr(chartWrapSignals(id)))
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

// hypnogramLanes is each sleep stage's lane (y, as % of plot height), top
// to bottom in Fitbit's order: awake, REM, light, deep.
var hypnogramLanes = []struct {
	Type string
	Y    float64
}{{"AWAKE", 8}, {"REM", 36}, {"LIGHT", 64}, {"DEEP", 92}}

func hypnogramLaneY(stageType string) (float64, bool) {
	for _, l := range hypnogramLanes {
		if l.Type == stageType {
			return l.Y, true
		}
	}
	return 0, false
}

// renderHypnogramSVG draws one sleep session the way Fitbit's sleep log
// does: a lane per stage, a thick rounded bar on the stage's lane for each
// segment, a thin vertical connector at every stage change, lane names on
// the left and 12-hour clock times along the bottom. Same plot frame and
// hover tooltip as the other charts (chartOpen/hoverAttrs).
func renderHypnogramSVG(stages []StageSegment, start, end time.Time, id string) string {
	if len(stages) == 0 || !end.After(start) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="chart-wrap hypnogram" data-signals="%s"><div class="chart-plot">`, escapeAttr(chartWrapSignals(id)))
	b.WriteString(`<svg viewBox="0 0 100 100" class="chart-svg" preserveAspectRatio="none">`)

	// Connectors go in first so the bars are drawn over their ends.
	var bars strings.Builder
	prevY, havePrev := 0.0, false
	for _, s := range stages {
		y, ok := hypnogramLaneY(s.Type)
		if !ok {
			continue
		}
		x1, x2 := clampPct(s.StartPct)*100, clampPct(s.EndPct)*100
		if havePrev && prevY != y {
			fmt.Fprintf(&b, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" class="hyp-connector"/>`, x1, prevY, x1, y)
		}
		prevY, havePrev = y, true
		fmt.Fprintf(&bars, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" class="hyp-bar stage-%s"/>`, x1, y, x2, y, strings.ToLower(s.Type))
		// Hover target: the segment's whole lane band — easier to hit than
		// the bar itself, which can be a single minute wide.
		fmt.Fprintf(&bars, `<rect x="%.2f" y="%.2f" width="%.2f" height="14" class="chart-hit-target" %s/>`,
			x1, y-7, max(x2-x1, 0.3), hoverAttrs(id, (x1+x2)/2, y, s.TimeLabel, humanizeStageType(s.Type)))
	}
	b.WriteString(bars.String())
	b.WriteString(`</svg>`)

	for _, l := range hypnogramLanes {
		fmt.Fprintf(&b, `<span class="chart-y-label" style="top:%.2f%%">%s</span>`, l.Y, l.Type)
	}
	// A tick on every hour; narrow charts thin them out (chartXLabelSpan).
	span := end.Sub(start)
	k := 0
	for t := start.Local().Truncate(time.Hour).Add(time.Hour); t.Before(end); t = t.Add(time.Hour) {
		chartXLabelSpan(&b, float64(t.Sub(start))/float64(span)*100, t.Format("3:04"), k)
		k++
	}
	chartClose(&b, id)
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
