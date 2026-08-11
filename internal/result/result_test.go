package result

import (
	"os"
	"testing"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
)

func TestWriteAtomic(t *testing.T) {
	value := model.Result{SchemaVersion: 1, Run: model.RunInfo{ID: "77aa4a1a-4f8f-42a9-816c-982e1cba80db", StartedAt: time.Now()}}
	path, err := WriteAtomic(t.TempDir(), value)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("unexpected JSON contents: %q", data)
	}
}
