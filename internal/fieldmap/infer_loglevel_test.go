package fieldmap

import (
	"reflect"
	"testing"
)

// TestInferFindsLogLevel: the IoT log indices name the level log_level (ops' own
// query, 2026-10, and the 1.x field maps users wrote by hand); infer finds it.
func TestInferFindsLogLevel(t *testing.T) {
	r := Infer("iot-pangu-test-log-*", []string{"@timestamp", "log_level", "log_app", "msg"}, nil)
	if !reflect.DeepEqual(r.LevelFields, []string{"log_level"}) || !reflect.DeepEqual(r.ServiceFields, []string{"log_app"}) {
		t.Fatalf("level %v, service %v", r.LevelFields, r.ServiceFields)
	}
}
