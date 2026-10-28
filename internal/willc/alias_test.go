package willc

import (
	"strings"
	"testing"
)

func TestEmitTypeAlias(t *testing.T) {
	src := "type UserId = i64\n\ndef load(id: UserId) -> i64 {\n    return id\n}\n"
	data := emitSource(t, "alias.will", src, "example.com/app")
	if !strings.Contains(string(data), `typealias parent=#1`) || !strings.Contains(string(data), `text="UserId"`) {
		t.Fatalf("alias record is missing:\n%s", data)
	}
	if _, err := Read(data); err != nil {
		t.Fatalf("read emitted alias: %v\n%s", err, data)
	}
}
