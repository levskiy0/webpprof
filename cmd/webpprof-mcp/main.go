package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/levskiy0/webpprof/client"
	"github.com/levskiy0/webpprof/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultProfilerURL = "http://127.0.0.1:6061/debug/webpprof/"

var version = "dev"

type config struct {
	profilerURL string
	token       string
	allowRemote bool
	showVersion bool
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "webpprof-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, getenv func(string) string, stdout, stderr io.Writer, transport mcp.Transport) error {
	configuration, err := parseConfig(arguments, getenv, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if configuration.showVersion {
		_, err := fmt.Fprintln(stdout, effectiveVersion())
		return err
	}
	if err := validateProfilerURL(configuration.profilerURL, configuration.allowRemote); err != nil {
		return err
	}

	profilerClient, err := client.New(configuration.profilerURL, client.WithToken(configuration.token))
	if err != nil {
		return fmt.Errorf("create profiler client: %w", err)
	}
	service, err := mcpserver.New(profilerClient)
	if err != nil {
		return fmt.Errorf("create MCP service: %w", err)
	}
	server := newServer(service, effectiveVersion())
	if err := server.Run(ctx, transport); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("serve MCP over stdio: %w", err)
	}
	return nil
}

func parseConfig(arguments []string, getenv func(string) string, stderr io.Writer) (config, error) {
	profilerURL := strings.TrimSpace(getenv("WEBPPROF_URL"))
	if profilerURL == "" {
		profilerURL = defaultProfilerURL
	}
	configuration := config{profilerURL: profilerURL, token: getenv("WEBPPROF_TOKEN")}
	flags := flag.NewFlagSet("webpprof-mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&configuration.profilerURL, "url", configuration.profilerURL, "webpprof base URL (env: WEBPPROF_URL)")
	flags.BoolVar(&configuration.allowRemote, "allow-remote", false, "allow a non-loopback HTTPS profiler URL")
	flags.BoolVar(&configuration.showVersion, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: webpprof-mcp [flags]\n\nRuns an MCP server over stdio and reads a running webpprof instance over HTTP.\nThe profiler token is read from WEBPPROF_TOKEN.\n\nFlags:\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		return config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return configuration, nil
}

func validateProfilerURL(rawURL string, allowRemote bool) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse profiler URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("profiler URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return errors.New("profiler URL host is required")
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "localhost" {
		return nil
	}
	if address := net.ParseIP(hostname); address != nil && address.IsLoopback() {
		return nil
	}
	if !allowRemote {
		return errors.New("profiler URL must use localhost or a loopback IP; use --allow-remote for an HTTPS remote profiler")
	}
	if parsed.Scheme != "https" {
		return errors.New("remote profiler URL must use https")
	}
	return nil
}

func effectiveVersion() string {
	if version != "dev" {
		return version
	}
	build, ok := debug.ReadBuildInfo()
	if !ok || build.Main.Version == "" || build.Main.Version == "(devel)" {
		return version
	}
	return build.Main.Version
}
