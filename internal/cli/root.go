package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"cha0s-sim/internal/admin"
	"cha0s-sim/internal/config"
	"cha0s-sim/internal/proxy"
)

var rootCmd = &cobra.Command{
	Use: "cha0s-sim",
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _ := cmd.Flags().GetString("target")
		port, _ := cmd.Flags().GetInt("port")
		preserveHost, _ := cmd.Flags().GetBool("preserve-host")
		insecureSkipVerify, _ := cmd.Flags().GetBool("insecure-skip-verify")
		verbose, _ := cmd.Flags().GetBool("verbose")
		configPath, _ := cmd.Flags().GetString("config")
		adminPort, _ := cmd.Flags().GetInt("admin-port")
		noAdmin, _ := cmd.Flags().GetBool("no-admin")

		u, err := config.ValidateTargetURL(target)
		if err != nil {
			return fmt.Errorf("invalid --target: %w", err)
		}

		if insecureSkipVerify {
			fmt.Fprintln(os.Stderr, "WARNING: --insecure-skip-verify is enabled. TLS certificate validation is DISABLED for the upstream target. Do not use this in production.")
		}

		var store *config.Store
		if cmd.Flags().Changed("config") || fileExists(configPath) {
			store, err = config.NewStore(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config %q: %w", configPath, err)
			}
			defer store.Close()
			for _, w := range store.Current().Warnings() {
				fmt.Fprintln(os.Stderr, "WARNING: "+w)
			}
		}

		return run(port, u, preserveHost, insecureSkipVerify, verbose, adminPort, !noAdmin, store)
	},
}

func run(port int, u *url.URL, preserveHost, insecureSkipVerify, verbose bool, adminPort int, runAdmin bool, store *config.Store) error {
	// SessionName is "" for CLI-originated events: the plain CLI proxy has no
	// session concept (it is not part of the desktop's session system). The
	// stdout sink prints security findings to stdout regardless of the flag;
	// per-request traffic lines are only shown with --verbose. The registry is
	// nil too: endpoint discovery is a desktop-session feature with no CLI UI.
	proxySrv := proxy.NewServerInstance(port, u, preserveHost, insecureSkipVerify, verbose, store, proxy.PipelineFull, "", nil, &stdoutSink{out: os.Stdout, verbose: verbose})

	errCh := make(chan error, 2)

	fmt.Fprintf(os.Stderr, "proxy listening on :%d\n", port)
	go func() {
		if err := proxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	var adminSrv *admin.Server
	if runAdmin {
		adminSrv = admin.NewServer(adminPort, store)
		go func() {
			if err := adminSrv.Start(); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("server failed: %w", err)
	case <-sigCh:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdownErr := proxySrv.Shutdown(ctx)
	if shutdownErr != nil {
		return fmt.Errorf("proxy shutdown: %w", shutdownErr)
	}
	if adminSrv != nil {
		if err := adminSrv.Shutdown(); err != nil {
			return fmt.Errorf("admin shutdown: %w", err)
		}
	}

	fmt.Fprintln(os.Stderr, "shutdown complete")
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func init() {
	rootCmd.Flags().String("target", "", "backend target URL, e.g. http://localhost:3000")
	rootCmd.Flags().Int("port", 8080, "port for cha0s-sim to listen on")
	rootCmd.Flags().BoolP("verbose", "v", false, "enables detailed logging")
	rootCmd.Flags().Bool("preserve-host", false, "preserve the original Host header instead of rewriting it to the target's host")
	rootCmd.Flags().Bool("insecure-skip-verify", false, "skip TLS certificate verification for the target (use only for local self-signed certs — insecure)")
	rootCmd.Flags().String("config", "chaos.yaml", "path to the chaos configuration file (yaml or json)")
	rootCmd.Flags().Int("admin-port", 8090, "port for the admin dashboard server to listen on")
	rootCmd.Flags().Bool("no-admin", false, "do not start the admin dashboard server")
	rootCmd.MarkFlagRequired("target")
}

func Execute() error {
	return rootCmd.Execute()
}
