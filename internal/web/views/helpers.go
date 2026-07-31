package views

import (
	"fmt"
	"strings"
)

// humanizeExerciseType turns Google Health's SCREAMING_SNAKE_CASE
// exercise type ("BIKING") into a display label ("Biking").
func humanizeExerciseType(t string) string {
	if t == "" {
		return "Activity"
	}
	words := strings.Split(strings.ToLower(t), "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// formatMinutes renders a minute count as "1h 5m" (or just "42m" under an
// hour), matching the reference dashboard's compact duration style.
func FormatMinutes(total int64) string {
	if total <= 0 {
		return "0m"
	}
	h, m := total/60, total%60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// formatNumber adds thousands separators, e.g. 6287 -> "6,287".
func FormatNumber(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
