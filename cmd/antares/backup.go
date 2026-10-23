package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/raufimusaddiq/antares/internal/store"
)

// isAdminCmd reports whether args name an offline database subcommand.
func isAdminCmd(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "backup", "restore", "check":
		return true
	}
	return false
}

// runAdminCmd runs `antares backup|restore|check`. These need only
// ANTARES_DB_PATH, not the full service configuration or any secret.
func runAdminCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	dbPath := getenv("ANTARES_DB_PATH")
	fs := flag.NewFlagSet(args[1], flag.ContinueOnError)
	fs.SetOutput(stderr)
	switch args[1] {
	case "backup":
		fs.Usage = func() { fmt.Fprintln(stderr, "usage: antares backup <dest-file | ->   ('-' streams to stdout)") }
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			fs.Usage()
			return errors.New("backup: want exactly one destination")
		}
		if dbPath == "" {
			return errors.New("ANTARES_DB_PATH is required")
		}
		db, err := store.OpenForBackup(ctx, dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		if dest := fs.Arg(0); dest == "-" {
			return store.BackupTo(ctx, db, dbPath, stdout)
		} else if err := store.Backup(ctx, db, dest); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "backup written and verified: %s\n", fs.Arg(0))
		return nil

	case "restore":
		keep := fs.String("keep-old", "", "directory OUTSIDE the data directory to copy the current database into before replacing it (a path in or under it is refused)")
		discard := fs.Bool("discard-old", false, "replace without keeping a copy of the current database")
		fs.Usage = func() {
			fmt.Fprintln(stderr, "usage: antares restore (-keep-old DIR | -discard-old) <backup-file>   (stop antares first)")
		}
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 1 || (*keep != "") == *discard {
			fs.Usage()
			return errors.New("restore: give a backup file and exactly one of -keep-old or -discard-old")
		}
		if dbPath == "" {
			return errors.New("ANTARES_DB_PATH is required")
		}
		set, err := store.LoadMigrations(store.Migrations())
		if err != nil {
			return err
		}
		if err := store.Restore(ctx, fs.Arg(0), dbPath, set, store.RestoreOptions{KeepOldDir: *keep}); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "restored %s from %s; start antares and check /readyz\n", dbPath, fs.Arg(0))
		return nil

	default: // check
		fs.Usage = func() {
			fmt.Fprintln(stderr, "usage: antares check [file]   (default: ANTARES_DB_PATH; stop antares first when checking the live database, the read-only open needs a writable -shm)")
		}
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		path := dbPath
		if fs.NArg() > 0 {
			path = fs.Arg(0)
		}
		if path == "" {
			return errors.New("give a file or set ANTARES_DB_PATH")
		}
		rep, err := store.Check(ctx, path)
		if err != nil {
			return fmt.Errorf("check %s: %w", path, err)
		}
		if !rep.OK() {
			fmt.Fprintf(stdout, "CORRUPT %s\n", path)
			for _, l := range append(rep.Integrity, rep.ForeignKey...) {
				fmt.Fprintln(stdout, "  "+strings.TrimSpace(l))
			}
			if rep.Truncated {
				fmt.Fprintln(stdout, "  (more problems exist than the 50 listed)")
			}
			return errors.New("integrity check failed; see docs/runbooks/backup-restore.md")
		}
		fmt.Fprintf(stdout, "ok %s\n", path)
		return nil
	}
}
