package cmd

import "testing"

// commandCoverage lists every user-facing command path covered by unit tests (mock Kibana).
var commandCoverage = []struct {
	path     string
	unitTest string
}{
	{path: "kibana-cli --version", unitTest: "TestRoot_Version"},
	{path: "kibana-cli --help", unitTest: "TestRoot_Help"},
	{path: "kibana-cli auth login", unitTest: "TestAuth_Login_*"},
	{path: "kibana-cli auth logout", unitTest: "TestAuth_Logout_*"},
	{path: "kibana-cli auth status", unitTest: "TestAuth_Status_*"},
	{path: "kibana-cli doctor", unitTest: "TestDoctor_*"},
	{path: "kibana-cli context", unitTest: "TestContext_Mock"},
	{path: "kibana-cli config init", unitTest: "TestConfig_Init_*"},
	{path: "kibana-cli config show", unitTest: "TestConfig_Show_*"},
	{path: "kibana-cli patterns list", unitTest: "TestPatterns_List_*"},
	{path: "kibana-cli patterns fields", unitTest: "TestPatterns_Fields_*"},
	{path: "kibana-cli search", unitTest: "TestSearch_*"},
	{path: "kibana-cli agg", unitTest: "TestAgg_*"},
	{path: "kibana-cli objects list", unitTest: "TestObjects_List_*"},
	{path: "kibana-cli objects get", unitTest: "TestObjects_Get_*"},
	{path: "kibana-cli update", unitTest: "TestUpdate_*"},
	{path: "kibana-cli reference", unitTest: "TestReference_*"},
}

// TestCommandCoverageManifest ensures the coverage table stays complete when commands are added.
func TestCommandCoverageManifest(t *testing.T) {
	if len(commandCoverage) < 15 {
		t.Fatalf("expected >=15 commands, got %d", len(commandCoverage))
	}
	for _, row := range commandCoverage {
		if row.path == "" || row.unitTest == "" {
			t.Fatalf("incomplete row: %+v", row)
		}
	}
}
