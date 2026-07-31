package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/paths"
)

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Manage the encrypted local database",
	}
	cmd.AddCommand(newDBInitCmd())
	cmd.AddCommand(newDBDecryptCmd())
	return cmd
}

func newDBInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "First-run setup: create the folder structure and the encrypted database",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := appPaths.EnsureDirs(); err != nil {
				return fmt.Errorf("creating folder structure: %w", err)
			}
			fmt.Println("created folder structure under", appPaths.Root())

			if err := config.WriteTemplateIfMissing(appPaths.ConfigFile()); err != nil {
				return fmt.Errorf("writing config template: %w", err)
			}

			var key crypto.Key
			if _, err := os.Stat(appPaths.DBKeyFile()); err == nil {
				fmt.Println("key material already exists at", appPaths.DBKeyFile(), "- reusing it")
				key, err = crypto.LoadKey(appPaths.DBKeyFile())
				if err != nil {
					return fmt.Errorf("loading existing key: %w", err)
				}
			} else {
				passphrase, err := promptNewPassphrase()
				if err != nil {
					return err
				}
				key, err = crypto.GenerateAndSaveKey(passphrase, appPaths.DBKeyFile())
				if err != nil {
					return fmt.Errorf("saving key material: %w", err)
				}
				fmt.Println("wrote key material to", appPaths.DBKeyFile())
			}

			if _, err := os.Stat(appPaths.DBFile()); err == nil {
				fmt.Println("encrypted database already exists at", appPaths.DBFile(), "- skipping")
				return nil
			}

			if err := db.Init(appPaths.DBFile(), appPaths.DBWorkingFile(), key); err != nil {
				return fmt.Errorf("creating encrypted database: %w", err)
			}
			fmt.Println("created encrypted database at", appPaths.DBFile())
			return nil
		},
	}
}

func newDBDecryptCmd() *cobra.Command {
	var confirm bool

	cmd := &cobra.Command{
		Use:   "decrypt <out.sql>",
		Short: "Dump the encrypted database to a plaintext .sql file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return errors.New("refusing to write plaintext data without --confirm")
			}

			out, err := appPaths.ExternalOutputPath(args[0])
			if err != nil {
				return err
			}
			if err := paths.EnsureParentDir(out); err != nil {
				return err
			}

			key, err := crypto.LoadKey(appPaths.DBKeyFile())
			if err != nil {
				return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
			}

			// Use a throwaway working file rather than DBWorkingFile() so
			// this read-only operation can never collide with (or clobber)
			// a server/sync process that has the real one open.
			tmp, err := os.CreateTemp(appPaths.DBDir(), ".health.db.decrypt-*")
			if err != nil {
				return fmt.Errorf("creating temporary working file: %w", err)
			}
			tmpPath := tmp.Name()
			tmp.Close()
			os.Remove(tmpPath) // db.Open must create it fresh, not find it pre-existing

			store, err := db.Open(appPaths.DBFile(), tmpPath, key)
			if err != nil {
				return fmt.Errorf("opening encrypted database: %w", err)
			}
			defer store.Discard()

			f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				return fmt.Errorf("creating output file %s: %w", out, err)
			}
			defer f.Close()

			fmt.Fprintln(os.Stderr, "WARNING: writing DECRYPTED, PLAINTEXT health data to", out)
			if err := db.Dump(store.DB(), f); err != nil {
				return fmt.Errorf("dumping database: %w", err)
			}

			fmt.Println("wrote plaintext SQL dump to", out)
			return nil
		},
	}

	cmd.Flags().BoolVar(&confirm, "confirm", false, "required: acknowledges this writes unencrypted data to disk")

	return cmd
}

// promptNewPassphrase reads and confirms a new passphrase for encrypting
// the database. HEALTHD_DB_PASSPHRASE bypasses the interactive masked
// prompt for unattended/scripted use (init on a fresh service install,
// test automation) — the same pattern tools like restic use for their
// password env var.
func promptNewPassphrase() (string, error) {
	if p := os.Getenv("HEALTHD_DB_PASSPHRASE"); p != "" {
		return p, nil
	}

	fmt.Print("Enter a passphrase to encrypt the database: ")
	pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading passphrase: %w", err)
	}

	fmt.Print("Confirm passphrase: ")
	pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading passphrase: %w", err)
	}

	if len(pw1) == 0 {
		return "", errors.New("passphrase must not be empty")
	}
	if string(pw1) != string(pw2) {
		return "", errors.New("passphrases did not match")
	}
	return string(pw1), nil
}
