package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func Directory() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache: %w", err)
	}
	return filepath.Join(base, "lihacloud-bench", "cache"), nil
}

func Status() (path string, files int, bytes int64, err error) {
	path, err = Directory()
	if err != nil {
		return "", 0, 0, err
	}
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			files++
			bytes += info.Size()
		}
		return nil
	})
	return
}

func Prune() (string, error) {
	path, err := Directory()
	if err != nil {
		return "", err
	}
	if filepath.Base(path) != "cache" || filepath.Base(filepath.Dir(path)) != "lihacloud-bench" {
		return "", errors.New("refusing to prune unexpected cache path")
	}
	if err := os.RemoveAll(path); err != nil {
		return "", fmt.Errorf("prune cache: %w", err)
	}
	return path, nil
}
