package web

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// logPollInterval bounds how quickly a newly written log line shows up in
// an open /settings/logs tab. A plain poll rather than a filesystem-
// notification library (fsnotify isn't already a dependency — see the
// ladder in CLAUDE.md's ponytail guidance): healthd's own log volume is a
// handful of lines a minute at most, so a 1s poll is imperceptible to
// someone watching it live and costs nothing between writes.
const logPollInterval = 1 * time.Second

// handleLogsPage serves the initial full-page view of the active log file
// (see applog's rotation policy — capped at ~1000 lines, so reading it
// whole on every page load is cheap, no last-N-lines windowing needed).
// The page itself opens a live tail (handleLogsStream) starting right
// where this snapshot ends, via the byte offset baked into the page.
func (s *Server) handleLogsPage(c echo.Context) error {
	data, err := os.ReadFile(s.Paths.LogFile())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.LogsPage(string(data), int64(len(data))).Render(c.Request().Context(), c.Response())
}

// handleLogsStream is a long-lived Datastar SSE connection (opened once,
// on page load, via LogsPage's data-init — see that template) that appends
// newly written log bytes to #log-content as they land on disk, polling
// the file's size rather than diffing content. offset is where the
// client's own page render already left off (see handleLogsPage), so nothing
// already shown is re-sent and nothing written between that render and this
// connection opening is missed. Scoped to the currently active log file
// only — a rotation (see applog.rotatingWriter) starts this file over from
// empty, which this handler treats the same as a manual clear (resets to
// the new, smaller file) rather than following the archived history; the
// archived file is still on disk, just not surfaced in this live view.
func (s *Server) handleLogsStream(c echo.Context) error {
	offset, _ := strconv.ParseInt(c.QueryParam("offset"), 10, 64)
	path := s.Paths.LogFile()

	sse := datastar.NewSSE(c.Response(), c.Request())
	ctx := sse.Context()

	ticker := time.NewTicker(logPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			chunk, newOffset, err := readLogTail(path, offset)
			if err != nil || len(chunk) == 0 {
				continue // file briefly missing/mid-rotation, or genuinely nothing new yet
			}
			offset = newOffset
			if err := sse.ExecuteScript(appendLogChunkJS(chunk)); err != nil {
				return nil // client disconnected
			}
		}
	}
}

// readLogTail reads whatever's been written to path since offset. A
// smaller current size than offset means the file was cleared or rotated
// out from under us — resync from the top rather than erroring, since
// what's "new" is now everything in the fresh file.
func readLogTail(path string, offset int64) (chunk []byte, newOffset int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}
	if info.Size() < offset {
		offset = 0
	}
	if info.Size() == offset {
		return nil, offset, nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	chunk, err = io.ReadAll(f)
	if err != nil {
		return nil, offset, err
	}
	return chunk, offset + int64(len(chunk)), nil
}

// appendLogChunkJS builds the inline script handleLogsStream pushes per
// poll: append chunk as a text node (preserving #log-content's existing
// content instead of re-rendering it, so an in-progress text selection/scroll
// isn't disturbed) and, only if the viewer was already scrolled to the
// bottom, follow the new content — same "don't yank the view if someone
// scrolled up to read history" rule a terminal's own tail -f follows.
// json.Marshal (not fmt.Sprintf/strconv.Quote) is what safely turns an
// arbitrary Go string into a JS string literal here — same pattern
// initialSignals already uses in urls.go.
func appendLogChunkJS(chunk []byte) string {
	jsText, _ := json.Marshal(string(chunk))
	return `(function(){
		var el = document.getElementById("log-content");
		if (!el) return;
		var atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 40;
		el.appendChild(document.createTextNode(` + string(jsText) + `));
		if (atBottom) { el.scrollTop = el.scrollHeight; }
	})();`
}

// handleLogsClear truncates the active log file to empty — a log a human
// is actively debugging against, not user health data, so this is a plain
// destructive action behind the same session auth as every other settings
// page, no extra confirmation step server-side (the page's own Clear
// button already confirms client-side before sending this).
//
// ponytail: truncating the file out from under applog's own open,
// append-mode *os.File handle (same process, different *os.File — Go/
// Windows both allow this) works, but leaves that writer's in-memory line
// count stale (it still thinks the pre-clear lines are there), so the next
// rotation may fire a bit earlier than maxLines would otherwise call for.
// Harmless — rotation is just an archival threshold — so not worth
// plumbing a reset hook into applog.rotatingWriter for unless that ever
// actually matters.
func (s *Server) handleLogsClear(c echo.Context) error {
	if err := os.Truncate(s.Paths.LogFile(), 0); err != nil && !os.IsNotExist(err) {
		return err
	}
	sse := datastar.NewSSE(c.Response(), c.Request())
	return sse.PatchElementTempl(views.LogContent(""))
}
