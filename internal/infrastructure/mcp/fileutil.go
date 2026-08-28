package mcp

import (
	"os"
	"time"
)

// fileStat returns the modification time of a file (cross-platform).
func fileStat(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
