// Command migrate is the explicit database migration step, kept separate from
// the API (the API never migrates on startup). It applies the SQL migrations in
// ./migrations using golang-migrate.
//
// Usage:
//
//	migrate up            # apply all pending migrations
//	migrate down          # roll back ALL migrations
//	migrate down <N>      # roll back N migrations
//	migrate version       # print current version + dirty flag
//
// Reads DATABASE_URL from the environment.
package main

import (
	"errors"
	"log"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // postgres:// driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // file:// source
)

const migrationsSource = "file://migrations"

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: migrate <up|down|version> [N]")
	}
	cmd := os.Args[1]

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
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
		if len(os.Args) >= 3 {
			n, perr := strconv.Atoi(os.Args[2])
			if perr != nil || n <= 0 {
				log.Fatalf("migrate down: invalid step count %q", os.Args[2])
			}
			if err := m.Steps(-n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				log.Fatalf("migrate down %d: %v", n, err)
			}
		} else {
			if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				log.Fatalf("migrate down: %v", err)
			}
		}
		log.Println("migrate down: done")

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
		log.Fatalf("migrate: unknown command %q (use up|down|version)", cmd)
	}
}
