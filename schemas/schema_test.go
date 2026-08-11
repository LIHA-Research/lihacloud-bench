package schemas_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/config"
	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

func TestResultModelConformsToPublishedSchema(t *testing.T) {
	now := time.Now().UTC()
	value := model.Result{
		SchemaVersion: 1,
		Tool:          model.ToolInfo{Version: "v0.1.0", Commit: "abc", BuildDate: now.Format(time.RFC3339)},
		Run:           model.RunInfo{ID: "77aa4a1a-4f8f-42a9-816c-982e1cba80db", Profile: "quick", Status: "success", StartedAt: now, EndedAt: &now},
		Host:          model.HostInfo{Label: "test", OS: "linux", Arch: "amd64", LogicalCPUs: 1},
		Plan:          model.Plan{Categories: []string{"cpu"}},
		Benchmarks:    []model.Benchmark{{ID: "cpu", Category: "cpu", Status: model.StatusSuccess, Engine: "builtin-sha256", Comparable: false, Metrics: []model.Metric{{Name: "throughput", Value: 1, Unit: "MiB/s", Direction: "higher"}}, Warnings: []string{}}},
		Resources:     []model.Resource{}, Warnings: []string{},
	}
	validateJSON(t, "result-v1.schema.json", value)
}

func TestExampleConformsToPublishedConfigSchema(t *testing.T) {
	var value any
	if err := yaml.Unmarshal([]byte(config.Example), &value); err != nil {
		t.Fatal(err)
	}
	validateJSON(t, "config-v1.schema.json", value)
}

func validateJSON(t *testing.T, schemaPath string, value any) {
	t.Helper()
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaData, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://example.invalid/schema.json"
	if err := compiler.AddResource(location, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(normalized); err != nil {
		t.Fatal(err)
	}
}
