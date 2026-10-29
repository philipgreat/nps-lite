package clientpackage

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestClientPackageSupervisor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix supervisor")
	}
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "npc_linux_amd64.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, _ := z.Create("npc")
	// Exit once, then stay alive, so we verify recovery and duplicate prevention.
	w.Write([]byte("#!/bin/sh\nif [ ! -f first-exit ]; then touch first-exit; exit 1; fi\necho $$ > active-pid\nexec sleep 60\n"))
	z.Close()
	f.Close()
	b, err := Open(dir, "linux", "amd64", Profile{"example.com:8025", "test-key", true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var buf bytes.Buffer
	if err := b.WriteZIP(&buf); err != nil {
		t.Fatal(err)
	}
	r, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	for _, entry := range r.File {
		in, _ := entry.Open()
		out, err := os.OpenFile(filepath.Join(dir, entry.Name), os.O_CREATE|os.O_WRONLY, entry.Mode())
		if err != nil {
			t.Fatal(err)
		}
		_, err = out.ReadFrom(in)
		in.Close()
		out.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "keep-running.sh")
	if out, err := exec.Command("sh", "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("shell syntax: %v %s", err, out)
	}
	cmd := exec.Command("sh", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
		}
	}()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "active-pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor did not recover after exit")
		}
		time.Sleep(50 * time.Millisecond)
	}
	duplicate := exec.Command("sh", script)
	duplicateDone := make(chan error, 1)
	if err := duplicate.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { duplicateDone <- duplicate.Wait() }()
	select {
	case err := <-duplicateDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		duplicate.Process.Kill()
		t.Fatal("duplicate supervisor did not exit")
	}
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	if _, err := os.Stat(filepath.Join(dir, ".npc-supervisor.lock")); !os.IsNotExist(err) {
		t.Fatal("lock was not cleaned up")
	}
	// Put completion back for deferred cleanup.
	done <- nil
}
