package cmd

import "testing"

// TestReference_EveryCommandHasRealSchemaAndExample guards against output_schema
// regressing to a stub: every command must reference a schema label that exists in
// the schemas map with a non-empty field list, and every leaf (non-group) command
// must carry at least one runnable example. This keeps `reference` a usable source
// of truth for agents.
func TestReference_EveryCommandHasRealSchemaAndExample(t *testing.T) {
	specs := collectCommandSpecs(rootCmd, "")
	if len(specs) == 0 {
		t.Fatal("reference enumerated zero commands")
	}
	schemas := referenceSchemas()
	for _, c := range specs {
		if c.OutputSchema == "" {
			t.Errorf("%s: empty output_schema", c.Path)
			continue
		}
		schema, ok := schemas[c.OutputSchema]
		if !ok {
			t.Errorf("%s: output_schema %q not defined in schemas map", c.Path, c.OutputSchema)
			continue
		}
		if len(schema.Fields) == 0 {
			t.Errorf("%s: schema %q has no fields (stub)", c.Path, c.OutputSchema)
		}
		// Group/parent commands emit only help; every other command must show how
		// to run it.
		if c.OutputSchema != "group" && len(c.Examples) == 0 {
			t.Errorf("%s: no examples", c.Path)
		}
	}
}

// TestReference_SchemasAllReferenced ensures the schemas map has no orphan labels:
// every schema must be claimed by at least one command, so the enumeration stays
// honest and in sync with real output.
func TestReference_SchemasAllReferenced(t *testing.T) {
	specs := collectCommandSpecs(rootCmd, "")
	used := map[string]bool{}
	for _, c := range specs {
		used[c.OutputSchema] = true
	}
	for label := range referenceSchemas() {
		if !used[label] {
			t.Errorf("schema %q is defined but no command references it", label)
		}
	}
}
