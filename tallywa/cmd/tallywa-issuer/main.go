// Command tallywa-issuer is the offline activation server. It is the only
// piece of cloud infrastructure required by the product. After a license
// is signed and emailed to the customer, this server is irrelevant to
// daily operation — desktops verify license.dat locally on every start.
//
// Subcommands:
//
//	keygen              Generate an Ed25519 keypair (run once, ever).
//	mint                Manually mint a paid token (for the first 50
//	                    customers before Razorpay is wired up).
//	serve               Run the HTTP API for /redeem and the
//	                    Razorpay webhook.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tallywa-issuer <keygen|mint|serve> [flags]")
	}
	switch args[0] {
	case "keygen":
		return runKeygen(args[1:])
	case "mint":
		return runMint(args[1:])
	case "serve":
		return runServe(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

// keygen prints a fresh Ed25519 keypair. Run ONCE, save the private key
// somewhere safe (1Password, secrets manager). The public key gets
// embedded into tallywa-svc at build time.
func runKeygen(_ []string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fmt.Println("# tallywa license signing keypair — keep PRIVATE_KEY secret")
	fmt.Println("PUBLIC_KEY_B64=" + base64.StdEncoding.EncodeToString(pub))
	fmt.Println("PRIVATE_KEY_B64=" + base64.StdEncoding.EncodeToString(priv))
	return nil
}

// mint manually creates a token. Used for early customers before the
// Razorpay webhook is online.
func runMint(args []string) error {
	fs := flag.NewFlagSet("mint", flag.ContinueOnError)
	dbPath := fs.String("db", "issuer.db", "SQLite database path")
	email := fs.String("email", "", "customer email (required)")
	plan := fs.String("plan", "pro", "plan name (lite|pro|business)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}
	planKey := strings.ToLower(*plan)
	spec, ok := Plans[planKey]
	if !ok {
		return fmt.Errorf("unknown plan %q (known: lite, pro, business)", *plan)
	}
	store, err := OpenStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := mintToken(ctx, store, mintArgs{
		Email: *email,
		Plan:  planKey,
		Spec:  spec,
	})
	if err != nil {
		return err
	}
	fmt.Println("Token minted:")
	fmt.Println("  code:        " + tok.Code)
	fmt.Println("  email:       " + tok.CustomerEmail)
	fmt.Println("  edition:     " + tok.Edition)
	fmt.Println("  redemptions: " + fmt.Sprint(tok.MaxRedemptions))
	return nil
}

// serve starts the HTTP API.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbPath := fs.String("db", "issuer.db", "SQLite database path")
	addr := fs.String("addr", ":8090", "listen address")
	privKeyEnv := fs.String("priv-env", "TALLYWA_LICENSE_PRIV", "env var holding base64 Ed25519 private key")
	rzpEnv := fs.String("razorpay-secret-env", "RAZORPAY_WEBHOOK_SECRET", "env var holding Razorpay webhook secret")
	if err := fs.Parse(args); err != nil {
		return err
	}

	privB64 := os.Getenv(*privKeyEnv)
	if privB64 == "" {
		return fmt.Errorf("env %s is empty (set it to your base64 Ed25519 private key)", *privKeyEnv)
	}
	privBytes, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return fmt.Errorf("decode %s: %w", *privKeyEnv, err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return fmt.Errorf("private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(privBytes))
	}

	store, err := OpenStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := NewServer(store, ed25519.PrivateKey(privBytes), []byte(os.Getenv(*rzpEnv)), logger)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logger.Info("issuer: listening", "addr", *addr, "db", *dbPath)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
