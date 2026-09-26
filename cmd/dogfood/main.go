package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"dogfood/internal/db"
	"dogfood/internal/httpapp"
	"dogfood/internal/migrations"
	"dogfood/internal/seed"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("healthcheck ok")
		os.Exit(0)
	}

	if err := runServer(); err != nil {
		fmt.Fprintf(os.Stderr, "server fatal error: %v\n", err)
		os.Exit(1)
	}
}

func runHealthcheck() error {
	portStr := os.Getenv("PORT")
	if portStr == "" {
		portStr = "8080"
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	url := fmt.Sprintf("http://127.0.0.1:%s/healthz", portStr)
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("connection error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("non-200 status code: %d", resp.StatusCode)
	}

	return nil
}

func runServer() error {
	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		val, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("invalid PORT environment variable %q: %w", p, err)
		}
		port = val
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "dogfood.db"
	}

	fmt.Printf("opening SQLite database at %s...\n", dbPath)
	database, err := db.Open(db.Config{DSN: dbPath})
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Println("running migrations...")
	if err := migrations.Run(ctx, database); err != nil {
		return fmt.Errorf("migration failure: %w", err)
	}

	fmt.Println("seeding fixtures...")
	seedRes, err := seed.Run(ctx, database, "")
	if err != nil {
		return fmt.Errorf("seed failure: %w", err)
	}
	if seedRes.AlreadySeeded {
		fmt.Printf("fixtures already seeded (hash: %s...)\n", seedRes.SourceHash[:8])
	} else {
		fmt.Printf("seeded fixtures for event %s: %d tracks, %d teams, %d projects\n",
			seedRes.EventID, seedRes.TracksCount, seedRes.TeamsCount, seedRes.ProjectsCount)
		fmt.Println("seeded test logins:")
		fmt.Println("  organizer    Cookie: session=org_7f2a")
		fmt.Println("  judge_a      Cookie: session=jdg_a_91bc")
		fmt.Println("  judge_b      Cookie: session=jdg_b_44de")
		fmt.Println("  participant  Cookie: session=prt_2e88")
	}

	server, err := httpapp.NewServer(httpapp.Config{
		Port: port,
		DB:   database,
	})
	if err != nil {
		return fmt.Errorf("failed to configure HTTP server: %w", err)
	}

	// Channel for graceful shutdown notification
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		fmt.Printf("server listening on :%d\n", port)
		if err := server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("server startup failed: %w", err)
	case sig := <-stop:
		fmt.Printf("received signal %v, shutting down gracefully...\n", sig)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}
		fmt.Println("server stopped gracefully")
		return nil
	}
}
