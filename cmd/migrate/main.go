// Command migrate is the explicit database migration step, kept separate from
// the API (the API never migrates on startup). It applies the SQL migrations in
// ./migrations using golang-migrate.
//
// Usage:
//
//	migrate up            # apply all pending migrations
//	migrate down <N>      # roll back N migrations (a count is REQUIRED)
//	migrate down-all      # roll back ALL migrations (destructive; explicit only)
//	migrate version       # print current version + dirty flag
//
// DATABASE_URL is read via internal/config (the only package that reads env).
package main

import (
	"errors"
	"log"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // postgres:// driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // file:// source

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
)

const migrationsSource = "file://migrations"

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: migrate <up|down N|down-all|version>")
	}
	cmd := os.Args[1]

	dbURL, err := config.DatabaseURL()
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}

	m, err := migrate.New(migrationsSource, dbURL)
	if err != nil {
		log.Fatalf("migrate: init: %v", err)
	}
	defer m.Close()

	switch cmd {
	case "up":
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("migrate up: %v", err)
		}
		log.Println("migrate up: done")

	case "down":
		// A count is REQUIRED — a bare "down" must never roll back everything.
		if len(os.Args) < 3 {
			log.Fatal("migrate down requires a count, e.g. 'down 1'. To roll back everything use 'down-all'.")
		}
		n, perr := strconv.Atoi(os.Args[2])
		if perr != nil || n <= 0 {
			log.Fatalf("migrate down: invalid count %q (want a positive integer)", os.Args[2])
		}
		if err := m.Steps(-n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("migrate down %d: %v", n, err)
		}
		log.Printf("migrate down %d: done", n)

	case "down-all":
		log.Println("WARNING: rolling back ALL migrations — this drops every table and is destructive.")
		if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("migrate down-all: %v", err)
		}
		log.Println("migrate down-all: done")

	case "version":
		v, dirty, verr := m.Version()
		if errors.Is(verr, migrate.ErrNilVersion) {
			log.Println("migrate version: none (no migrations applied)")
			return
		}
		if verr != nil {
			log.Fatalf("migrate version: %v", verr)
		}
		log.Printf("migrate version: %d (dirty=%v)", v, dirty)

	default:
		log.Fatalf("migrate: unknown command %q (use up|down N|down-all|version)", cmd)
	}
}
