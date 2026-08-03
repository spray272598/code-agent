package observability

import (
	"log"
	"os"
	"strings"
	"sync/atomic"
)

// Log levels: debug=0 info=1 warn=2 error=3
var logLevel atomic.Int32

func init() {
	SetLogLevel("info")
}

func SetLogLevel(level string) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "trace":
		logLevel.Store(0)
	case "info", "":
		logLevel.Store(1)
	case "warn", "warning":
		logLevel.Store(2)
	case "error":
		logLevel.Store(3)
	default:
		logLevel.Store(1)
	}
}

func LogLevel() string {
	switch logLevel.Load() {
	case 0:
		return "debug"
	case 2:
		return "warn"
	case 3:
		return "error"
	default:
		return "info"
	}
}

func Debugf(format string, args ...any) {
	if logLevel.Load() <= 0 {
		log.Printf("[debug] "+format, args...)
	}
}

func Infof(format string, args ...any) {
	if logLevel.Load() <= 1 {
		log.Printf("[info] "+format, args...)
	}
}

func Warnf(format string, args ...any) {
	if logLevel.Load() <= 2 {
		log.Printf("[warn] "+format, args...)
	}
}

func Errorf(format string, args ...any) {
	log.Printf("[error] "+format, args...)
}

// ApplyFromEnv reads LOG_LEVEL.
func ApplyLogLevelFromEnv() {
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		SetLogLevel(v)
	}
}
