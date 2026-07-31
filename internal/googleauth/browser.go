package googleauth

import (
	"os/exec"
	"runtime"
)

// openBrowserFunc is a package-level seam so tests can stub out actually
// launching a browser while exercising the rest of the consent flow.
var openBrowserFunc = openBrowser

// openBrowser best-effort launches the system's default browser at url. A
// failure here is never fatal to the auth flow — the URL is always also
// printed so the user can open it themselves.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
