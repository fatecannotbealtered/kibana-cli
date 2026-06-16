package fieldmap

import (
	"strings"
	"testing"
)

func TestInfer_TraceField(t *testing.T) {
	r := Infer("legacy-log-*",
		[]string{"@timestamp", "log_app", "level", "msg", "log_traceId"},
		nil,
	)
	if r.TraceMode != TraceModeField {
		t.Fatalf("trace mode = %q", r.TraceMode)
	}
	if len(r.ServiceFields) == 0 || r.ServiceFields[0] != "log_app" {
		t.Fatalf("service fields = %v", r.ServiceFields)
	}
	if len(r.TraceIDFields) == 0 || r.TraceIDFields[0] != "log_traceId" {
		t.Fatalf("trace fields = %v", r.TraceIDFields)
	}
	if len(r.MessageFields) == 0 || r.MessageFields[0] != "msg" {
		t.Fatalf("message fields = %v", r.MessageFields)
	}
}

func TestInfer_TraceInMsg(t *testing.T) {
	r := Infer("v3-log-*",
		[]string{"@timestamp", "service_name", "level", "msg"},
		[]string{"level=INFO traceId=4bf92f3577b34da6a3ce929d0e0e4736 started"},
	)
	if r.TraceMode != TraceModeMsg {
		t.Fatalf("expected msg mode, got %q (notes=%v)", r.TraceMode, r.Notes)
	}
	if len(r.TraceIDFields) != 0 {
		t.Fatalf("should not set trace fields in msg mode: %v", r.TraceIDFields)
	}
	// In msg mode the profile omits trace_id_fields.
	p := r.Profile()
	if len(p.TraceIDFields) != 0 {
		t.Fatalf("profile trace fields should be empty in msg mode: %v", p.TraceIDFields)
	}
}

func TestInfer_NoTraceAnywhere(t *testing.T) {
	r := Infer("plain-*",
		[]string{"@timestamp", "message"},
		[]string{"just a plain log line with no ids"},
	)
	if r.TraceMode != TraceModeField {
		t.Fatalf("expected default field mode, got %q", r.TraceMode)
	}
	if len(r.Notes) == 0 {
		t.Fatal("expected a confidence note")
	}
}

func TestInferYAMLSnippet(t *testing.T) {
	r := Infer("sysA-app-*", []string{"log_app", "msg", "level", "traceId"}, nil)
	snip, err := r.YAMLSnippet("sysA-app")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"profiles:", "sysA-app:", "log_app", "trace_mode: field"} {
		if !strings.Contains(snip, want) {
			t.Fatalf("snippet missing %q:\n%s", want, snip)
		}
	}
}

func TestSuggestProfileName(t *testing.T) {
	cases := map[string]string{
		"sysA-app-*":      "sysA-app",
		"logs-*":          "logs",
		"*":               "inferred",
		"a.b.c-2024.*":    "a-b-c-2024",
		"platform_b-log*": "platform_b-log",
	}
	for in, want := range cases {
		if got := SuggestProfileName(in); got != want {
			t.Fatalf("SuggestProfileName(%q) = %q want %q", in, got, want)
		}
	}
}
