package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// allUserIDs lists every account's id — used by the sync scheduler
// (googlehealthsync.go/cronometersync.go) to fan out one sync pass per
// user, independent of who (if anyone) is currently logged into the
// dashboard (see ARCHITECTURE.md's multi-user section).
func allUserIDs(ctx context.Context, conn *sql.DB) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// resolveUserID resolves a --user username flag to its id, for the
// "healthd auth google/cronometer" commands, which act on one specific
// account's per-user credential files.
func resolveUserID(ctx context.Context, conn *sql.DB, username string) (int64, error) {
	if username == "" {
		return 0, errors.New("--user is required (create an account first via the dashboard's signup page, or \"healthd user create\")")
	}
	var id int64
	err := conn.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("no such user %q", username)
	}
	if err != nil {
		return 0, fmt.Errorf("looking up user %q: %w", username, err)
	}
	return id, nil
}

// newUserCmd lets an account be created headlessly (mirrors the dashboard's
// own /signup form — same webauth.CreateUser call), for setups where
// opening the dashboard first isn't convenient (e.g. scripted provisioning).
func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage healthd accounts"}
	cmd.AddCommand(newUserCreateCmd())
	return cmd
}

func newUserCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <username>",
		Short: "Create a new account (prompts for a password)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]

			key, err := crypto.LoadKey(appPaths.DBKeyFile())
			if err != nil {
				return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
			}
			store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), key)
			if err != nil {
				return fmt.Errorf("opening database (run \"healthd db init\" first?): %w", err)
			}
			defer func() {
				if cerr := store.Close(); cerr != nil {
					fmt.Fprintln(os.Stderr, "warning: closing database:", cerr)
				}
			}()

			fmt.Print("Password: ")
			pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return fmt.Errorf("reading password: %w", err)
			}
			fmt.Print("Confirm password: ")
			pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return fmt.Errorf("reading password: %w", err)
			}
			if string(pw1) != string(pw2) {
				return errors.New("passwords did not match")
			}

			user, err := webauth.CreateUser(context.Background(), store.DB(), appPaths, username, string(pw1))
			if err != nil {
				return fmt.Errorf("creating user: %w", err)
			}
			if err := store.Checkpoint(); err != nil {
				return fmt.Errorf("checkpointing database: %w", err)
			}

			fmt.Printf("created account %q (id %d) — connect Google Health/Cronometer with \"healthd auth google --user %s\" / \"healthd auth cronometer --user %s\", or log in at the dashboard to use its onboarding flow\n",
				user.Username, user.ID, username, username)
			return nil
		},
	}
}
