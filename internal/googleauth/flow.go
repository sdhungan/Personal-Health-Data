// Package googleauth implements the local-redirect OAuth2 flow for the
// Google Health API described in ARCHITECTURE.md §5, plus encrypted token
// storage and an auto-refreshing, auto-persisting HTTP client for the sync
// job to use afterwards.
package googleauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

// consentTimeout bounds how long a consent flow waits for the user to
// approve (or deny) access in the browser before giving up.
const consentTimeout = 5 * time.Minute

type callbackResult struct {
	code string
	err  error
}

// ConsentFlow is an in-progress local-redirect OAuth consent flow: AuthURL
// is where to send the user's browser, and Wait blocks until that browser
// completes it (approves, denies, or the flow times out or the listener
// fails) and exchanges the resulting code for a token. Built by
// StartConsentFlow, which does none of that waiting itself — callers
// decide how the user actually reaches AuthURL: RunConsentFlow (CLI use,
// "healthd auth google") opens a system browser and blocks immediately; a
// web-triggered connect instead redirects the browser tab that's already
// open (see internal/web/auth.go's startGoogleConnect) and waits in the
// background, since there's no desktop session to pop a *new* browser
// window from once the dashboard is running as an OS service.
type ConsentFlow struct {
	AuthURL string
	Wait    func(ctx context.Context) (*oauth2.Token, error)
}

// StartConsentFlow starts the local callback listener and builds the
// consent URL, without opening a browser or blocking for the result — see
// ConsentFlow's doc comment for why this is split out from RunConsentFlow.
// successRedirect, if non-empty, is where the callback sends the browser
// after a successful exchange (a full URL, e.g. back to the dashboard)
// instead of the plain "you can close this tab" text RunConsentFlow's CLI
// callers get.
func StartConsentFlow(clientJSON []byte, callbackPort int, successRedirect string) (*ConsentFlow, error) {
	oauthCfg, err := OAuthConfig(clientJSON, callbackPort)
	if err != nil {
		return nil, err
	}

	state, err := randomState()
	if err != nil {
		return nil, fmt.Errorf("generating CSRF state: %w", err)
	}

	results := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		if errParam := query.Get("error"); errParam != "" {
			fmt.Fprintln(w, "Authorization denied. You can close this tab.")
			results <- callbackResult{err: fmt.Errorf("authorization denied by user: %s", errParam)}
			return
		}
		if got := query.Get("state"); got != state {
			http.Error(w, "invalid state parameter", http.StatusBadRequest)
			results <- callbackResult{err: errors.New("received callback with mismatched CSRF state")}
			return
		}
		code := query.Get("code")
		if code == "" {
			http.Error(w, "missing code parameter", http.StatusBadRequest)
			results <- callbackResult{err: errors.New("callback had no authorization code")}
			return
		}

		if successRedirect != "" {
			http.Redirect(w, r, successRedirect, http.StatusFound)
		} else {
			fmt.Fprintln(w, "Authorization complete. You can close this tab and return to healthd.")
		}
		results <- callbackResult{code: code}
	})

	addr := fmt.Sprintf("127.0.0.1:%d", callbackPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("starting local callback listener on %s: %w", addr, err)
	}

	server := &http.Server{Handler: mux}
	serveErrs := make(chan error, 1)
	go func() {
		serveErrs <- server.Serve(listener)
	}()

	// prompt=select_account forces Google's account chooser to show every
	// time, instead of silently reusing whatever Google account is already
	// active in the browser — important here since one machine/browser can
	// easily be signed into several Google accounts and different healthd
	// accounts may want to connect different ones. "consent" is combined
	// with it (space-separated, both values in one prompt param) so a
	// refresh token is still always issued, not just on first-ever
	// authorization — Google otherwise omits it on a repeat auth for an
	// account that already granted access before.
	authURL := oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "select_account consent"))

	wait := func(ctx context.Context) (*oauth2.Token, error) {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()

		select {
		case res := <-results:
			if res.err != nil {
				return nil, res.err
			}
			token, err := oauthCfg.Exchange(ctx, res.code)
			if err != nil {
				return nil, fmt.Errorf("exchanging authorization code for a token: %w", err)
			}
			return token, nil
		case err := <-serveErrs:
			return nil, fmt.Errorf("local callback listener failed: %w", err)
		case <-time.After(consentTimeout):
			return nil, fmt.Errorf("timed out after %s waiting for authorization", consentTimeout)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return &ConsentFlow{AuthURL: authURL, Wait: wait}, nil
}

// RunConsentFlow starts a local callback listener, opens the Google
// consent screen in a system browser, and blocks until the exchange
// completes — the CLI-appropriate shape ("healthd auth google --user X",
// run directly in a terminal with its own attached desktop session, where
// popping a real browser window actually works). It does not persist the
// token — callers pass the result to SaveToken.
func RunConsentFlow(ctx context.Context, clientJSON []byte, callbackPort int) (*oauth2.Token, error) {
	flow, err := StartConsentFlow(clientJSON, callbackPort, "")
	if err != nil {
		return nil, err
	}

	fmt.Println("Open this URL to authorize healthd with your Google Health data (opening your browser now):")
	fmt.Println(flow.AuthURL)
	if err := openBrowserFunc(flow.AuthURL); err != nil {
		fmt.Println("(couldn't open a browser automatically — open the URL above manually)")
	}

	return flow.Wait(ctx)
}

func randomState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
