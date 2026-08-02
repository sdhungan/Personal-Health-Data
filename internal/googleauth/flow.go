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

// consentTimeout bounds how long RunConsentFlow waits for the user to
// approve (or deny) access in the browser before giving up.
const consentTimeout = 5 * time.Minute

type callbackResult struct {
	code string
	err  error
}

// RunConsentFlow starts a local callback listener, opens the Google
// consent screen, and exchanges the resulting code for a token. It does
// not persist the token — callers are expected to pass the result to
// SaveToken. clientJSON is the app-wide OAuth client (see LoadClientJSON);
// callbackPort is config.yaml's google.callback_port.
func RunConsentFlow(ctx context.Context, clientJSON []byte, callbackPort int) (*oauth2.Token, error) {
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

		fmt.Fprintln(w, "Authorization complete. You can close this tab and return to healthd.")
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
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
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
	fmt.Println("Open this URL to authorize healthd with your Google Health data (opening your browser now):")
	fmt.Println(authURL)
	if err := openBrowserFunc(authURL); err != nil {
		fmt.Println("(couldn't open a browser automatically — open the URL above manually)")
	}

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

func randomState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
