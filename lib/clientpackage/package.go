// Package clientpackage creates per-client installation bundles from prebuilt ZIPs.
package clientpackage

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Profile struct {
	ServerAddr string `json:"server_addr"`
	VKey       string `json:"vkey"`
	TLSEnable  bool   `json:"tls_enable"`
}

func (p Profile) Validate() error {
	host, port, err := net.SplitHostPort(p.ServerAddr)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || host == "" || n < 1 || n > 65535 || strings.ContainsAny(host, " /\\\r\n\t") {
		return errors.New("invalid client server address (expected hostname:port)")
	}
	if p.VKey == "" || len(p.VKey) > 4096 || strings.ContainsAny(p.VKey, "\x00\r\n") {
		return errors.New("invalid client VKey")
	}
	if !p.TLSEnable {
		return errors.New("installation profiles require TLS")
	}
	return nil
}

func ReadProfile(path string) (Profile, error) {
	var p Profile
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 16385))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&p); err != nil {
		return p, errors.New("invalid connection profile JSON")
	}
	var extra interface{}
	if decoder.Decode(&extra) != io.EOF {
		return p, errors.New("unexpected data after connection profile")
	}
	return p, p.Validate()
}

func BinaryName(platform, arch string) (string, error) {
	if platform != "windows" && platform != "darwin" && platform != "linux" {
		return "", errors.New("unsupported operating system")
	}
	if arch != "amd64" && arch != "arm64" {
		return "", errors.New("only AMD64 and ARM64 are supported")
	}
	if platform == "windows" {
		return "npc.exe", nil
	}
	return "npc", nil
}

type Bundle struct {
	archive  *zip.ReadCloser
	binary   *zip.File
	platform string
	profile  Profile
}

// Open validates everything before the HTTP response begins. Archive paths are
// selected from a strict six-platform allowlist, never from caller filenames.
func Open(dir, platform, arch string, p Profile) (*Bundle, error) {
	name, err := BinaryName(platform, arch)
	if err != nil {
		return nil, err
	}
	if err = p.Validate(); err != nil {
		return nil, err
	}
	r, err := zip.OpenReader(filepath.Join(dir, "npc_"+platform+"_"+arch+".zip"))
	if err != nil {
		return nil, err
	}
	if len(r.File) != 1 || r.File[0].Name != name || r.File[0].UncompressedSize64 > 100<<20 || r.File[0].FileInfo().IsDir() || (r.File[0].Method != zip.Deflate && r.File[0].Method != zip.Store) {
		r.Close()
		return nil, errors.New("invalid prebuilt NPC archive")
	}
	return &Bundle{r, r.File[0], platform, p}, nil
}

func (b *Bundle) Close() error { return b.archive.Close() }

func (b *Bundle) WriteZIP(w io.Writer) error {
	z := zip.NewWriter(w)
	// Copy compressed bytes directly: no recompression or full-binary buffering.
	if err := z.Copy(b.binary); err != nil {
		return err
	}
	profile, err := json.MarshalIndent(b.profile, "", "  ")
	if err != nil {
		return err
	}
	if err = add(z, "connection.json", append(profile, '\n'), 0600); err != nil {
		return err
	}
	name := "start.sh"
	script := "#!/bin/sh\nset -eu\ncd \"$(dirname \"$0\")\"\nchmod +x ./npc\nexec ./npc -connection_profile ./connection.json -debug=true -log_level=6\n"
	if b.platform == "darwin" {
		name = "start.command"
	}
	if b.platform == "windows" {
		name = "start.bat"
		script = "@echo off\r\ncd /d \"%~dp0\"\r\nnpc.exe -connection_profile \"connection.json\" -debug=true -log_level=6\r\npause\r\n"
	}
	if err = add(z, name, []byte(script), 0755); err != nil {
		return err
	}
	readme := fmt.Sprintf("NPS Lite client / NPC 客户端\n\nServer / 服务端: %s\nTLS: enabled / 已启用\n\nExtract all files to one folder. / 请将所有文件解压到同一目录。\nWindows: double-click start.bat.\nmacOS: open start.command, or run: sh start.command\nLinux: run: sh start.sh\n\nconnection.json contains this client's VKey. Share this package only with the intended client.\nconnection.json 包含该客户端 VKey，请仅将安装包交给对应客户端。\nThis starts NPC in the foreground; it does not install a system service.\n启动脚本在前台运行，不会自动安装系统服务。\nOn macOS, approve downloaded software through the system's normal security prompts if requested.\nmacOS 如提示安全确认，请按系统正常流程允许运行。\n", b.profile.ServerAddr)
	if err = add(z, "README.txt", []byte(readme), 0644); err != nil {
		return err
	}
	return z.Close()
}

func add(z *zip.Writer, name string, data []byte, mode os.FileMode) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, err := z.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
