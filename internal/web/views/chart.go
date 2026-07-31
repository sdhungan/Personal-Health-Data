package views

import (
	"fmt"
	"strings"
)

// renderBarChartSVG renders c as a small inline bar chart with an
// optional goal line — server-rendered so no client-side charting
// library is needed, consistent with the rest of this project's
// "backend renders, browser displays" approach.
func renderBarChartSVG(c ChartData) string {
	const (
		width      = 340
		height     = 160
		leftAxis   = 34
		bottomAxis = 20
		topPad     = 10
	)
	plotW := float64(width - leftAxis - 10)
	plotH := float64(height - bottomAxis - topPad)

	maxVal := 0.0
	for _, v := range c.Values {
		if v > maxVal {
			maxVal = v
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
	axisMax := niceCeiling(maxVal)

	n := len(c.Values)
	if n == 0 {
		return ""
	}
	barGap := plotW / float64(n) * 0.25
	barW := plotW/float64(n) - barGap

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="chart-svg" preserveAspectRatio="xMidYMid meet">`, width, height)

	// Gridlines + axis labels (0 and axisMax).
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`,
		leftAxis, int(topPad), leftAxis, height-bottomAxis)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`,
		leftAxis, height-bottomAxis, width-5, height-bottomAxis)
	fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-axis-label">%s</text>`, int(topPad)+6, formatAxisNumber(axisMax))
	fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-axis-label">0</text>`, height-bottomAxis+4)

	// Goal line.
	if c.Goal != nil {
		goalY := topPad + plotH*(1-*c.Goal/axisMax)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" class="chart-goal-line"/>`,
			leftAxis, goalY, width-5, goalY)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" class="chart-goal-label">Goal: %s</text>`,
			width-140, goalY-6, formatAxisNumber(*c.Goal))
	}

	// Bars.
	for i, v := range c.Values {
		x := float64(leftAxis) + float64(i)*(barW+barGap) + barGap/2
		barH := plotH * (v / axisMax)
		y := topPad + plotH - barH
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" class="chart-bar"/>`, x, y, barW, barH)
		if i < len(c.Labels) {
			fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="chart-bar-label">%s</text>`,
				x+barW/2, height-4, escapeSVGText(c.Labels[i]))
		}
	}

	b.WriteString(`</svg>`)
	return b.String()
}

// renderLineChartSVG renders a simple heart-rate-over-time line, used by
// the activity detail overlay.
func renderLineChartSVG(samples []HRSample) string {
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

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="chart-svg" preserveAspectRatio="xMidYMid meet">`, width, height)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`, leftAxis, int(topPad), leftAxis, height-bottomAxis)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`, leftAxis, height-bottomAxis, width-5, height-bottomAxis)
	fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-axis-label">%s</text>`, int(topPad)+6, formatAxisNumber(maxV))
	fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-axis-label">%s</text>`, height-bottomAxis+4, formatAxisNumber(minV))

	n := len(samples)
	var points strings.Builder
	for i, s := range samples {
		x := float64(leftAxis) + plotW*float64(i)/float64(max(n-1, 1))
		y := topPad + plotH*(1-(s.Bpm-minV)/(maxV-minV))
		if i > 0 {
			points.WriteByte(' ')
		}
		fmt.Fprintf(&points, "%.1f,%.1f", x, y)
	}
	fmt.Fprintf(&b, `<polyline points="%s" class="chart-line"/>`, points.String())
	b.WriteString(`</svg>`)
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
