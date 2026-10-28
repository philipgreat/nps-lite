package clientpackage

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestClientPackageSixPlatforms(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(platform+"/"+arch, func(t *testing.T) {
				dir := t.TempDir()
				name, _ := BinaryName(platform, arch)
				path := filepath.Join(dir, "npc_"+platform+"_"+arch+".zip")
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				z := zip.NewWriter(f)
				h := &zip.FileHeader{Name: name, Method: zip.Deflate}
				h.SetMode(0755)
				w, _ := z.CreateHeader(h)
				payload := bytes.Repeat([]byte("precompiled NPC"), 1000)
				w.Write(payload)
				z.Close()
				f.Close()
				profile := Profile{ServerAddr: "remote-access.example.com:8025", VKey: "quotes\" & %PATH% $(touch NEVER) , key", TLSEnable: true}
				bundle, err := Open(dir, platform, arch, profile)
				if err != nil {
					t.Fatal(err)
				}
				defer bundle.Close()
				var output bytes.Buffer
				if err = bundle.WriteZIP(&output); err != nil {
					t.Fatal(err)
				}
				r, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
				if err != nil {
					t.Fatal(err)
				}
				if len(r.File) != 4 {
					t.Fatalf("expected four package files, got %d", len(r.File))
				}
				files := map[string][]byte{}
				for _, entry := range r.File {
					s, err := entry.Open()
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(s)
					s.Close()
					if err != nil {
						t.Fatal(err)
					}
					files[entry.Name] = data
				}
				if !bytes.Equal(files[name], payload) {
					t.Fatal("binary changed")
				}
				var got Profile
				if err = json.Unmarshal(files["connection.json"], &got); err != nil || got != profile {
					t.Fatal("profile changed", err)
				}
				startup := "start.sh"
				if platform == "darwin" {
					startup = "start.command"
				}
				if platform == "windows" {
					startup = "start.bat"
				}
				if !bytes.Contains(files[startup], []byte("-connection_profile")) || bytes.Contains(files[startup], []byte(profile.VKey)) {
					t.Fatal("unsafe startup script")
				}
				if platform != "windows" && r.File[2].Mode().Perm() != 0755 {
					t.Fatal("startup script is not executable")
				}
			})
		}
	}
}

func TestClientPackageRejectInvalidInput(t *testing.T) {
	profile := Profile{ServerAddr: "example.com:8025", VKey: "test-key", TLSEnable: true}
	for _, pair := range [][2]string{{"../linux", "amd64"}, {"linux", "386"}, {"windows", "arm"}, {"darwin", "../../file"}} {
		if b, err := Open(t.TempDir(), pair[0], pair[1], profile); err == nil {
			b.Close()
			t.Fatal("accepted invalid platform", pair)
		}
	}
	for _, p := range []Profile{{"example.com:0", "key", true}, {"https://example.com:8025", "key", true}, {"example.com:8025", "", true}, {"example.com:8025", "key", false}} {
		if p.Validate() == nil {
			t.Fatal("accepted invalid profile")
		}
	}
}

func TestClientPackageReadProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.json")
	want := Profile{ServerAddr: "[::1]:8025", VKey: "a\"%&,$()", TLSEnable: true}
	data, _ := json.Marshal(want)
	os.WriteFile(path, data, 0600)
	got, err := ReadProfile(path)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	for _, bad := range []string{string(data) + " {}", `{"server_addr":"example.com:8025","vkey":"key","tls_enable":false}`, `{"server_addr":"example.com:8025","vkey":"key","tls_enable":true,"extra":"field"}`} {
		os.WriteFile(path, []byte(bad), 0600)
		if _, err := ReadProfile(path); err == nil {
			t.Fatal("accepted invalid JSON profile")
		}
	}
}
