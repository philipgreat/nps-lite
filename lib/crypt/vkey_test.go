package crypt

import (
	"encoding/hex"
	"testing"
)

func TestVkeyRandomFormat(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 10000; i++ {
		key := GetVkey()
		if len(key) != 32 {
			t.Fatalf("VKey length = %d", len(key))
		}
		if _, err := hex.DecodeString(key); err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatal("duplicate VKey")
		}
		seen[key] = true
	}
}
