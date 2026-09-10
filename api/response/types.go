package response

// Response 统一响应结构
type Response struct {
	Code int         `json:"code" example:"0"`   // 状态码：0-成功，其他-失败
	Data interface{} `json:"data"`               // 响应数据
	Msg  string      `json:"msg" example:"获取成功"` // 响应消息
}

// PageResult 分页响应结构
type PageResult struct {
	List     interface{} `json:"list"`                  // 数据列表
	Total    int64       `json:"total" example:"100"`   // 总数
	Page     int         `json:"page" example:"1"`      // 当前页
	PageSize int         `json:"pageSize" example:"10"` // 每页大小
}
