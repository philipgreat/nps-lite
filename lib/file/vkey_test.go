package file

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVkeyPreservedAcrossNewClientAndReload(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "conf"), 0700); err != nil {
		t.Fatal(err)
	}
	db := &DbUtils{JsonDb: NewJsonDb(dir)}
	old := &Client{VerifyKey: "0123456789", Status: true}
	if err := db.NewClient(old); err != nil {
		t.Fatal(err)
	}
	fresh := &Client{Status: true}
	if err := db.NewClient(fresh); err != nil {
		t.Fatal(err)
	}
	if len(fresh.VerifyKey) != 32 {
		t.Fatalf("new key length = %d", len(fresh.VerifyKey))
	}
	reloaded := NewJsonDb(dir)
	reloaded.LoadClientFromJsonFile()
	for _, want := range []*Client{old, fresh} {
		got, err := reloaded.GetClient(want.Id)
		if err != nil || got.VerifyKey != want.VerifyKey {
			t.Fatalf("key changed for client %d", want.Id)
		}
	}
	if old.VerifyKey != "0123456789" {
		t.Fatal("existing short key changed")
	}
}
