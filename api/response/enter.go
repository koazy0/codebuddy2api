package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 业务状态码
const (
	CodeSuccess      = 0
	CodeFail         = 1
	CodeBadRequest   = 400
	CodeUnauthorized = 401
	CodeForbidden    = 403
	CodeNotFound     = 404
	CodeServerError  = 500
)

// Success 成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code: CodeSuccess,
		Data: data,
		Msg:  "操作成功",
	})
}

// SuccessWithMsg 成功响应（自定义消息）
func SuccessWithMsg(c *gin.Context, data interface{}, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: CodeSuccess,
		Data: data,
		Msg:  msg,
	})
}

// SuccessWithPage 分页成功响应
func SuccessWithPage(c *gin.Context, list interface{}, total int64, page, pageSize int) {
	c.JSON(http.StatusOK, Response{
		Code: CodeSuccess,
		Data: PageResult{
			List:     list,
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		},
		Msg: "操作成功",
	})
}

// Fail 失败响应
func Fail(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: CodeFail,
		Data: nil,
		Msg:  msg,
	})
}

// FailWithCode 失败响应（自定义错误码）
func FailWithCode(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: code,
		Data: nil,
		Msg:  msg,
	})
}

// FailWithData 失败响应（带数据）
func FailWithData(c *gin.Context, code int, data interface{}, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: code,
		Data: data,
		Msg:  msg,
	})
}

// BadRequest 参数错误
func BadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: CodeBadRequest,
		Data: nil,
		Msg:  msg,
	})
}

// Unauthorized 未授权
func Unauthorized(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: CodeUnauthorized,
		Data: nil,
		Msg:  msg,
	})
}

// Forbidden 禁止访问
func Forbidden(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: CodeForbidden,
		Data: nil,
		Msg:  msg,
	})
}

// NotFound 未找到
func NotFound(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: CodeNotFound,
		Data: nil,
		Msg:  msg,
	})
}

// InternalServerError 服务器错误
func InternalServerError(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Response{
		Code: CodeServerError,
		Data: nil,
		Msg:  msg,
	})
}
