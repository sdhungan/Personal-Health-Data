package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/mcpserver"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// newMCPCmd runs a local stdio MCP server exposing Cronometer food
// search/logging tools for one account — spawned as a subprocess by an MCP
// host (Claude Code, Claude Desktop) rather than started as a standing
// service, so it has no --action lifecycle flag unlike the merged
// scheduler+dashboard process: its lifecycle is owned entirely by whatever
// host spawns it. Deliberately stayed stdio rather than moving to an HTTP
// route on the merged service (tried and reverted 2026-08-05, see
// prerequisite.md): Claude Desktop's own config file only understands a
// spawned command/args entry, not an arbitrary URL, so an HTTP transport
// would have needed a separate bridge process (and a new runtime
// dependency) for zero actual benefit — this was never one of the two
// competing OS services the "merge everything into one process" work
// (service.go) was actually about; it was already a per-session spawned
// subprocess with no install/start/stop of its own, same as `healthd sync`
// remains its own one-shot subcommand rather than folded into the merged
// process. See ARCHITECTURE.md's MCP connector section for the full design.
//
// --user follows the exact pattern "healthd auth google/cronometer --user"
// already established in auth.go: resolves a username to its account, then
// loads that account's per-user credential key (the same one the
// background sync scheduler already uses unattended, see ARCHITECTURE.md
// §10) to decrypt its saved Cronometer credentials. No password prompt
// here — this reuses whatever was already saved by "healthd auth
// cronometer --user <name>" or the dashboard's Cronometer login card.
func newMCPCmd() *cobra.Command {
	var username string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run a local stdio MCP server exposing Cronometer food search/logging tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			dbKey, err := crypto.LoadKey(appPaths.DBKeyFile())
			if err != nil {
				return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
			}
			store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), dbKey)
			if err != nil {
				return fmt.Errorf("opening database (run \"healthd db init\" first?): %w", err)
			}
			// Every diagnostic below goes to stderr, never stdout — stdout is
			// the MCP JSON-RPC wire once srv.Run takes it over.
			defer func() {
				if cerr := store.Close(); cerr != nil {
					fmt.Fprintln(os.Stderr, "warning: closing database:", cerr)
				}
			}()

			userID, err := resolveUserID(ctx, store.DB(), username)
			if err != nil {
				return err
			}
			userKey, err := webauth.CredentialKey(appPaths, userID)
			if err != nil {
				return fmt.Errorf("loading credential key for %q: %w", username, err)
			}

			syncer, err := cronometer.NewDBSyncer(store.DB(), userID, userKey,
				appPaths.UserCronometerCredentialsFile(userID), appPaths.UserCronometerSessionFile(userID))
			if err != nil {
				return fmt.Errorf("loading Cronometer credentials for %q (run \"healthd auth cronometer --user %s\" first?): %w", username, username, err)
			}

			srv := mcpserver.New(syncer)
			return srv.Run(ctx, &mcpsdk.StdioTransport{})
		},
	}

	cmd.Flags().StringVar(&username, "user", "", "healthd account username whose Cronometer connection this MCP server logs food into (required)")
	return cmd
}
