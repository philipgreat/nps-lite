package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"ehang.io/nps/bridge"
	"ehang.io/nps/lib/clientpackage"
	"ehang.io/nps/lib/common"
	"ehang.io/nps/lib/file"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

func canDownloadClient(authenticated, admin bool, ownerID, id int) bool {
	return authenticated && id > 0 && (admin || ownerID == id)
}

// Downloadpackage returns a session-authorized, per-client ZIP. Keys never
// appear in the request URL, package filename, access logs or shared static files.
func (s *ClientController) Downloadpackage() {
	s.Ctx.Output.Header("Cache-Control", "private, no-store")
	s.Ctx.Output.Header("Pragma", "no-cache")
	if s.Ctx.Request.Method != http.MethodGet {
		s.Abort("405")
		return
	}
	id := s.GetIntNoErr("id")
	admin, _ := s.GetSession("isAdmin").(bool)
	owner, _ := s.GetSession("clientId").(int)
	if !canDownloadClient(s.GetSession("auth") == true, admin, owner, id) {
		s.Abort("403")
		return
	}
	platform, arch := s.GetString("platform"), s.GetString("arch")
	if _, err := clientpackage.BinaryName(platform, arch); err != nil {
		s.CustomAbort(400, "Unsupported platform or architecture")
		return
	}
	c, err := file.GetDb().GetClient(id)
	if err != nil {
		s.Abort("404")
		return
	}
	if !bridge.ServerTlsEnable {
		s.CustomAbort(503, "TLS bridge is disabled")
		return
	}
	server := beego.AppConfig.String("client_package_server")
	dir := beego.AppConfig.DefaultString("client_package_dir", filepath.Join(common.GetRunPath(), "client-binaries"))
	bundle, err := clientpackage.Open(dir, platform, arch, clientpackage.Profile{ServerAddr: server, VKey: c.VerifyKey, TLSEnable: true})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.CustomAbort(503, "Requested NPC binary has not been installed")
			return
		}
		s.CustomAbort(503, "Client package is not configured correctly")
		return
	}
	defer bundle.Close()
	s.EnableRender = false
	s.Ctx.Output.Header("Content-Type", "application/zip")
	s.Ctx.Output.Header("X-Content-Type-Options", "nosniff")
	s.Ctx.Output.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"npc-client-%d-%s-%s.zip\"", id, platform, arch))
	if err = bundle.WriteZIP(s.Ctx.ResponseWriter); err != nil {
		logs.Error("NPC package streaming failed for client %d: %v", id, err)
	}
}
