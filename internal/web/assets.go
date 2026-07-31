package web

import "embed"

// staticFS embeds everything the dashboard needs to render — the
// self-hosted Datastar bundle and the stylesheet — so the server has no
// runtime dependency on a CDN or the filesystem next to the binary,
// consistent with ARCHITECTURE.md's single-binary, fully-local intent.
//
//go:embed static/datastar.js static/style.css
var staticFS embed.FS
