package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join("..", "schema", "oba-validator-report.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("report.json", doc); err != nil {
		t.Fatalf("add resource: %v", err)
	}
	sch, err := c.Compile("report.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return sch
}

func validateAgainst(t *testing.T, sch *jsonschema.Schema, v any) error {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var inst any
	if err := json.Unmarshal(b, &inst); err != nil {
		t.Fatal(err)
	}
	return sch.Validate(inst)
}

func TestSchema_SuccessDocumentConforms(t *testing.T) {
	sch := compileSchema(t)
	if err := validateAgainst(t, sch, BuildDocument(sampleReport(), sampleConfig(), fixedTime())); err != nil {
		t.Errorf("success document failed schema:\n%v", err)
	}
}

func TestSchema_ErrorDocumentConforms(t *testing.T) {
	sch := compileSchema(t)
	ed := ErrorDocument{SchemaVersion: SchemaVersion, Error: "boom"}
	if err := validateAgainst(t, sch, ed); err != nil {
		t.Errorf("error document failed schema:\n%v", err)
	}
}

func TestSchema_RejectsMalformed(t *testing.T) {
	sch := compileSchema(t)
	// Has schemaVersion but is neither a valid report (missing meta/summary/groups)
	// nor a valid error (no "error"); oneOf must match zero variants.
	bad := map[string]any{"schemaVersion": "1.0", "summary": map[string]any{}}
	if err := validateAgainst(t, sch, bad); err == nil {
		t.Error("expected malformed document to fail schema validation")
	}
}
