package llamaconfig

import (
	"reflect"
	"testing"
)

func TestTTLs(t *testing.T) {
	got, err := TTLs(`
globalTTL: 600
models:
  a:
    cmd: x
  b:
    cmd: x
    ttl: 0
  c:
    cmd: x
    ttl: 120
  d:
    cmd: x
    ttl: -1
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"a": 600, "b": 0, "c": 120, "d": 600}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	got, _ = TTLs("models:\n  a:\n    cmd: x\n")
	if got["a"] != 0 {
		t.Errorf("no ttl anywhere = %d, want 0", got["a"])
	}
}
