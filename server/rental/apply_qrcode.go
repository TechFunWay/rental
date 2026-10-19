// apply_qrcode.go — 租客登记链接的二维码图片（GitHub issue #5 需求 2）。
//
// 用户的场景：把二维码打印出来贴到房门/公告栏，租客拿手机一扫就能打开登记页
// 自己填资料，不用房东一条条转发链接。所以这里需要的是**一张能保存、能打印的
// 图片**，而不是页面上画个字。
//
// 为什么在后端生成而不是前端画：
//   - 房东要「下载后打印/贴墙」，后端直接给 PNG 最省事，前端不用引二维码库；
//   - 二维码内容就是登记链接本身（含网关域直连地址的推导逻辑，见 applyLinkBase），
//     这段推导只在后端有，前端再实现一遍必然走偏。
//
// 正确性要点：
//   - 二维码内容**必须与弹窗里显示的链接完全一致**，否则打印出去扫不开；
//     因此与 handleRoomApplyLink 共用同一套 applyLinkBase / token 逻辑；
//   - 生成前确保 token 已存在（没有就按「生成链接」的同样规则建一个），
//     避免用户先点「下载二维码」得到一个空 token 的废码。
package rental

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"smallgo/server/response"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// applyQRCodeSize 二维码图片边长（像素）。
//
// 480 是「A4 打印后约 4cm 见方」的稳妥值：手机在中距离也能扫上，
// 文件又只有几 KB。太小（<256）打印出来容易扫不动，太大纯属浪费。
const applyQRCodeSize = 480

// applyQRCodeFilename 下载时的文件名：RFC 5987 编码后的中文名（现代浏览器用它），
// applyQRCodeFilenameASCII 是给老浏览器/老系统的 ASCII 回退名。
//
// 为什么要两个：Content-Disposition 的 ASCII 回退如果直接把中文逐字替换成下划线，
// 会得到 "_______.png" 这种在下载目录里根本认不出来的名字（实测就是如此）。
// 给一个英文回退名（rental-apply-qrcode-<房号>.png）更实用。
const (
	applyQRCodeFilename      = "租客登记二维码"
	applyQRCodeFilenameASCII = "rental-apply-qrcode"
)

// handleRoomApplyQRCode 输出该房登记链接的二维码 PNG。
//
// 查询参数：
//
//	regenerate=1  强制换新 token（旧链接与旧二维码同时失效），与生成链接的语义一致
//	download=1    带 Content-Disposition 触发浏览器下载；不带则直接内联显示给 <img>
func handleRoomApplyQRCode(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		// 生成二维码不改数据（除非要建 token），但 token 一旦泄漏就能被陌生人
		// 打开登记页，所以与「生成链接」同档要求写权限。
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleRooms, PermEdit) {
			return
		}

		if c.Query("regenerate") == "1" || room.ApplyToken == "" {
			if err := ensureApplyToken(db, room, c.Query("regenerate") == "1"); err != nil {
				response.ErrorInternal(c, "生成登记二维码失败")
				return
			}
		}

		// 与 handleRoomApplyLink 共用基址推导：二维码扫出来的地址必须和弹窗里
		// 显示的链接一字不差，否则打印出去就扫不开。
		base, _, _ := applyLinkBase(c, db)
		link := strings.TrimRight(base, "/") + "/apply/" + room.ApplyToken

		png, err := qrcode.Encode(link, qrcode.Medium, applyQRCodeSize)
		if err != nil {
			response.ErrorInternal(c, "生成二维码图片失败")
			return
		}

		// 内容算出来了才写头，避免出错时已经写了 image/png 却返 500 文本。
		//
		// 文件名带房号，房东下载多间房的二维码时不会互相覆盖、也好认：
		//   inline   → 直接内联给 <img> 预览
		//   download → attachment 存文件
		filename := applyQRCodeFilename + "-" + room.RoomNo + ".png"
		kind := "inline"
		if c.Query("download") == "1" {
			kind = "attachment"
		}
		asciiFallback := applyQRCodeFilenameASCII + "-" + sanitizeASCIIRoomNo(room.RoomNo) + ".png"
		c.Header("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`,
			kind, asciiFallback, url.PathEscape(filename)))
		c.Header("Cache-Control", "no-store") // token 可能刚换过，别让浏览器吃到旧图
		c.Data(http.StatusOK, "image/png", png)
	}
}

// sanitizeASCIIRoomNo 把房号里不能进 ASCII 文件名的字符换掉（中文房号退化为 qr）。
func sanitizeASCIIRoomNo(roomNo string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
			return r
		default:
			return -1 // 丢掉：房号里常见的中文/斜杠/空格都不安全
		}
	}, strings.TrimSpace(roomNo))
	if cleaned == "" {
		return "qr"
	}
	return cleaned
}
