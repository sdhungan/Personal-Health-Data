package web

import (
	"fmt"

	"github.com/labstack/echo/v4"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// handleMCPConnectorPage serves a static, per-account page explaining how
// to wire this healthd instance into Claude Code/Desktop as a local MCP
// server (see internal/cli/mcp.go, internal/mcpserver) — purely
// visualization/setup info, no entry form, no chat UI: all natural-
// language/photo food logging happens in the MCP host's own chat, not in
// this dashboard (see ARCHITECTURE.md's MCP connector section for why).
// Per-account (not app-wide like /settings/google-client) because the
// config snippet it shows is specific to *this* logged-in account's own
// username.
func (s *Server) handleMCPConnectorPage(c echo.Context) error {
	userID := webauth.CurrentUserID(c)
	username := webauth.CurrentUsername(c)

	exePath := s.ExecutablePath
	if exePath == "" {
		exePath = "healthd" // best-effort fallback if os.Executable() failed at startup
	}
	root := s.Paths.Root()

	cliSnippet := fmt.Sprintf(`claude mcp add --transport stdio healthd-cronometer -- %q mcp --root %q --user %q`, exePath, root, username)
	jsonSnippet := fmt.Sprintf(`{
  "mcpServers": {
    "healthd-cronometer": {
      "command": %q,
      "args": ["mcp", "--root", %q, "--user", %q]
    }
  }
}`, exePath, root, username)

	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.MCPConnectorPage(username, cliSnippet, jsonSnippet, s.cronometerConnected(userID)).Render(c.Request().Context(), c.Response())
}
