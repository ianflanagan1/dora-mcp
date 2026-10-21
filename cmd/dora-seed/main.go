// Command dora-seed creates a SQLite database of made-up DORA records, for
// developing and demonstrating dora-mcp without real data.
//
// Usage:
//
//	dora-seed [-db path] [-seed n] [-force]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/ianflanagan1/dora-mcp/internal/dora"
	"github.com/ianflanagan1/dora-mcp/internal/seed"
	"github.com/ianflanagan1/dora-mcp/internal/store"
)

// The data covers a fixed year, so the same seed always gives the same file.
var window = dora.Window{
	From: time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC),
	To:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "dora-seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("dora-seed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "data/dora.sqlite", "path of the database to create")
	seedValue := flags.Uint64("seed", 1, "random seed; the same seed gives the same records")
	force := flags.Bool("force", false, "replace the database if it already exists")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *force {
		if err := os.Remove(*dbPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	err := store.Create(ctx, *dbPath, seed.Generate(*seedValue, window))
	if errors.Is(err, store.ErrExists) {
		return fmt.Errorf("%w (use -force to replace it)", err)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "created %s with seed %d, covering %s to %s\n",
		*dbPath, *seedValue, window.From.Format(time.DateOnly), window.To.AddDate(0, 0, -1).Format(time.DateOnly))
	return err
}
