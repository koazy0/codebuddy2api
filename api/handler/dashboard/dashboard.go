package dashboard

import (
	"net/http"
	"os"
	"path/filepath"

	"codebuddy-gateway/global"
	"codebuddy-gateway/web"

	"github.com/gin-gonic/gin"
)

// asset 在 dev 模式下从磁盘实时读取前端文件，改完刷新浏览器即可生效；
// 生产模式继续用编译期内嵌的副本，行为保持不变。
func asset(devPath string, embedded []byte) []byte {
	if !global.CORE_DEV {
		return embedded
	}
	raw, err := os.ReadFile(filepath.Join("web", devPath))
	if err != nil {
		return embedded
	}
	return raw
}

// Index 渲染控制台外壳；数据都走 /admin/* 接口，页面本身不带密钥。
func Index(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if global.CORE_DEV {
		c.Header("Cache-Control", "no-store, must-revalidate")
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", asset("index.html", web.IndexHTML))
}

func AppCSS(c *gin.Context) {
	if global.CORE_DEV {
		c.Header("Cache-Control", "no-store, must-revalidate")
	} else {
		c.Header("Cache-Control", "public, max-age=300")
	}
	c.Data(http.StatusOK, "text/css; charset=utf-8", asset("app.css", web.AppCSS))
}

func AppJS(c *gin.Context) {
	if global.CORE_DEV {
		c.Header("Cache-Control", "no-store, must-revalidate")
	} else {
		c.Header("Cache-Control", "public, max-age=300")
	}
	c.Data(http.StatusOK, "application/javascript; charset=utf-8", asset("app.js", web.AppJS))
}

// Title 让外部脚本能拿到当前面板标题（未配置时回落默认值）。
func Title() string {
	return global.CORE_CONFIG.Dashboard.TitleOrDefault()
}
