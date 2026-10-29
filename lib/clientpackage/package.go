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
	keepName := "keep-running.sh"
	keepScript := `#!/bin/sh
set -eu
cd "$(dirname "$0")"
# One supervisor per extracted package, even when cron runs every minute.
lock=".npc-supervisor.lock"
if ! mkdir "$lock" 2>/dev/null; then
    if [ ! -f "$lock/pid" ]; then exit 0; fi
    owner=$(cat "$lock/pid")
    case "$owner" in ''|*[!0-9]*) exit 1;; esac
    if kill -0 "$owner" 2>/dev/null; then exit 0; fi
    rm -f "$lock/pid"
    rmdir "$lock" 2>/dev/null || exit 0
    mkdir "$lock" 2>/dev/null || exit 0
fi
echo "$$" > "$lock/pid"
child=""
cleanup() {
    trap - EXIT HUP INT TERM
    if [ -n "$child" ]; then kill "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; fi
    rm -f "$lock/pid"
    rmdir "$lock" 2>/dev/null || true
    exit 0
}
trap cleanup EXIT HUP INT TERM
while :; do
    sh ./start.sh &
    child=$!
    wait "$child" || true
    sleep 5 &
    child=$!
    wait "$child" || true
done
`
	if b.platform == "darwin" {
		keepScript = strings.ReplaceAll(keepScript, "sh ./start.sh", "sh ./start.command")
	}
	if b.platform == "windows" {
		keepName = "keep-running.bat"
		keepScript = "@echo off\r\ncd /d \"%~dp0\"\r\n:restart\r\nnpc.exe -connection_profile \"connection.json\" -debug=true -log_level=6\r\ntimeout /t 5 /nobreak >nul\r\ngoto restart\r\n"
	}
	if err = add(z, keepName, []byte(keepScript), 0755); err != nil {
		return err
	}
	readme := fmt.Sprintf(`NPC 客户端 / NPS Lite client

服务端 / Server: %s
TLS 默认开启 / TLS enabled

文件说明 / Package contents
- npc / npc.exe：客户端执行文件 / NPC binary
- connection.json：已配置服务端地址、当前客户端 Key 和 TLS / Preconfigured server, client VKey and TLS
- %s：手动启动（前台运行）/ Manual foreground startup
- %s：退出后每 5 秒重试的保持运行脚本 / Supervisor, retries 5 seconds after exit
- README.txt：本说明 / These instructions

将所有文件解压到同一个固定目录，保留原有文件名。
Extract all files into one permanent folder; keep the original filenames.
先手动运行启动脚本确认连接。Windows 双击 start.bat；macOS 执行 sh start.command；Linux 执行 sh start.sh。
First verify connectivity using start.bat (Windows), sh start.command (macOS), or sh start.sh (Linux).
停止手动运行后，再设置后台保持运行，避免同时启动多个客户端。
Stop the manual instance before enabling the supervisor; do not run multiple copies.

Linux / macOS：cron 由使用者自行设置 / Configure cron yourself
此安装包不会自动修改 crontab，也不会安装系统服务。
This package does not modify crontab or install a system service.
运行 crontab -e，加入以下示例（务必把 /absolute/path/npc-client 改为实际绝对路径）：
Run crontab -e and add this example, replacing /absolute/path/npc-client with the real absolute path:
* * * * * /bin/sh "/absolute/path/npc-client/keep-running.sh" >> "/absolute/path/npc-client/npc.log" 2>&1
cron 每分钟检查；保持运行脚本在客户端退出后重试，并防止同一目录重复启动监督进程。
Cron checks every minute; the supervisor retries after NPC exits and prevents duplicate supervisors in the same directory.
目录路径不要包含百分号（cron 会特殊处理）；日志文件请自行轮转或清理。
Avoid percent signs in the directory path (special meaning in cron); rotate or clean up npc.log yourself.
停止后台运行：先删除对应 cron 项，再向 .npc-supervisor.lock/pid 中的进程发送 TERM。
To stop: remove the cron entry, then send TERM to the PID in .npc-supervisor.lock/pid.
macOS 也可自行使用 launchd。下载的软件如遇安全提示，请按系统正常流程确认。
On macOS, launchd is an alternative. Follow normal OS security prompts for downloaded software.

Windows：没有 cron；可自行通过任务计划程序在登录或开机时启动 keep-running.bat。
Windows: use Task Scheduler to run keep-running.bat at logon or startup; there is no cron.
只安排一个保持运行任务，不要重复打开多个脚本窗口。
Schedule only one supervisor task; do not open multiple script windows.

connection.json 包含客户端 Key，请仅将安装包交给对应客户端。
connection.json contains the client VKey. Share this package only with the intended client.
`, b.profile.ServerAddr, name, keepName)
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
