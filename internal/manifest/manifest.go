package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const SchemaVersion = 1

type Manifest struct {
	SchemaVersion int        `json:"schema_version"`
	RunID         string     `json:"run_id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Resources     []Resource `json:"resources"`
}

type Resource struct {
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Marker     string            `json:"marker"`
	Properties map[string]string `json:"properties,omitempty"`
}

type Store struct {
	dir string
}

func NewStore() (Store, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return Store{}, fmt.Errorf("resolve user cache: %w", err)
	}
	return Store{dir: filepath.Join(cache, "lihacloud-bench", "runs")}, nil
}

func NewStoreAt(dir string) Store { return Store{dir: dir} }

func New(runID string) Manifest {
	now := time.Now().UTC()
	return Manifest{SchemaVersion: SchemaVersion, RunID: runID, CreatedAt: now, UpdatedAt: now, Resources: []Resource{}}
}

func (s Store) Path(runID string) string { return filepath.Join(s.dir, runID+".json") }

func (s Store) Save(value Manifest) error {
	if value.SchemaVersion != SchemaVersion || value.RunID == "" {
		return errors.New("invalid manifest identity")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	value.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create manifest temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.Path(value.RunID)); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	return nil
}

func (s Store) Load(runID string) (Manifest, error) {
	data, err := os.ReadFile(s.Path(runID))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var value Manifest
	if err := json.Unmarshal(data, &value); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if value.SchemaVersion != SchemaVersion || value.RunID != runID {
		return Manifest{}, errors.New("manifest identity mismatch")
	}
	return value, nil
}

func (s Store) Delete(runID string) error {
	err := os.Remove(s.Path(runID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
