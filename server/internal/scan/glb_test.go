package scan

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func readGLB(t *testing.T, b []byte) (map[string]any, []byte) {
	t.Helper()
	if len(b) < 20 || binary.LittleEndian.Uint32(b) != 0x46546C67 || binary.LittleEndian.Uint32(b[4:]) != 2 {
		t.Fatal("not a glTF 2 binary")
	}
	if int(binary.LittleEndian.Uint32(b[8:])) != len(b) {
		t.Fatalf("length field %d, file %d", binary.LittleEndian.Uint32(b[8:]), len(b))
	}
	jl := int(binary.LittleEndian.Uint32(b[12:]))
	var doc map[string]any
	if err := json.Unmarshal(b[20:20+jl], &doc); err != nil {
		t.Fatal(err)
	}
	bl := int(binary.LittleEndian.Uint32(b[20+jl:]))
	return doc, b[28+jl : 28+jl+bl]
}
