// Command dora-mcp is an MCP server that gives read-only access to DORA
// records held in a SQLite database.
//
// By default it talks MCP over stdin and stdout, for an MCP client such as
// Claude Code to launch as a child process. With -transport=http it serves
// MCP's Streamable HTTP transport instead.
//
// Usage:
//
//	dora-mcp [-db path] [-transport stdio|http] [-addr host:port]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ianflanagan1/dora-mcp/internal/server"
	"github.com/ianflanagan1/dora-mcp/internal/store"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// stdout carries the MCP protocol when serving over stdio, so everything
	// else, logs included, goes to stderr.
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "dora-mcp:", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("dora-mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "data/dora.sqlite", "path to the SQLite database")
	transport := flags.String("transport", "stdio", `"stdio", or "http" for Streamable HTTP`)
	addr := flags.String("addr", "127.0.0.1:8080", "address to listen on with -transport=http")
	if err := flags.Parse(args); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	st, err := store.Open(ctx, *dbPath)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w (create it with: devbox run -- task seed)", err)
	}
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	srv := server.New(st, version)

	switch *transport {
	case "stdio":
		return srv.Run(ctx, &mcp.StdioTransport{})
	case "http":
		return serveHTTP(ctx, srv, *addr, logger)
	default:
		return fmt.Errorf("unknown transport %q", *transport)
	}
}

// serveHTTP serves MCP over Streamable HTTP until ctx is cancelled. It has no
// authentication, so it must only listen where every caller is trusted.
func serveHTTP(ctx context.Context, srv *mcp.Server, addr string, logger *slog.Logger) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	httpServer := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	errs := make(chan error, 1)
	go func() { errs <- httpServer.ListenAndServe() }()
	logger.Info("serving MCP over Streamable HTTP", "url", "http://"+addr, "auth", "none")

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
