package mysql

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func Open(dsn string, autoMigrate bool, schemaPath string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if autoMigrate && schemaPath != "" {
		if err := migrate(db, schemaPath); err != nil {
			// non-fatal: tables may already exist without CREATE DATABASE privilege
			fmt.Printf("[mysql] migrate warn: %v\n", err)
		}
	}
	return db, nil
}

func migrate(db *sql.DB, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// strip CREATE DATABASE / USE for non-root envs; run statements one by one
	sqlText := string(b)
	parts := strings.Split(sqlText, ";")
	for _, p := range parts {
		stmt := strings.TrimSpace(p)
		if stmt == "" {
			continue
		}
		up := strings.ToUpper(stmt)
		if strings.HasPrefix(up, "CREATE DATABASE") || strings.HasPrefix(up, "USE ") {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			// ignore duplicate (table/column already exists)
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "already exists") && !strings.Contains(msg, "duplicate column") {
				return fmt.Errorf("%w\nstmt: %s", err, truncate(stmt, 120))
			}
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
