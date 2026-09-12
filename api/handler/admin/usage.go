package admin

import (
	"strconv"
	"strings"
	"time"

	"codebuddy-gateway/api/response"
	"codebuddy-gateway/model"

	"github.com/gin-gonic/gin"
)

// usageFilterFromQuery 把 dashboard 的查询串翻译成数据库条件。
// 列表、批量删除、导出共用这一套解析，保证「看到的就是删掉的」。
func usageFilterFromQuery(c *gin.Context) model.UsageFilter {
	accountID, _ := parseID(c.Query("account_id"))
	filter := model.UsageFilter{
		AccountID: accountID,
		Model:     strings.TrimSpace(c.Query("model")),
		Protocol:  strings.TrimSpace(c.Query("protocol")),
		Status:    strings.TrimSpace(c.Query("status")),
		Keyword:   strings.TrimSpace(c.Query("keyword")),
	}
	filter.Start = parseTimeQuery(c.Query("start"))
	filter.End = parseTimeQuery(c.Query("end"))
	return filter
}

func parseTimeQuery(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if ts > 1e12 {
			ts = ts / 1000
		}
		t := time.Unix(ts, 0)
		return &t
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return &t
		}
	}
	return nil
}

func ListUsage(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	filter := usageFilterFromQuery(c)
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize

	list, total, err := model.ListUsageLogs(filter)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.SuccessWithPage(c, list, total, page, pageSize)
}

func GetUsage(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	item, err := model.GetUsageLog(id)
	if err != nil {
		response.NotFound(c, "usage not found")
		return
	}
	response.Success(c, item)
}

func DeleteUsage(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	if err := model.DeleteUsageLog(id); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": 1, "id": id})
}

// DeleteUsageByFilter 支持按条件批量清理；无条件的全清走 DELETE /admin/usage/all。
func DeleteUsageByFilter(c *gin.Context) {
	filter := usageFilterFromQuery(c)
	if filter.AccountID == 0 && filter.Model == "" && filter.Protocol == "" &&
		filter.Status == "" && filter.Keyword == "" && filter.Start == nil && filter.End == nil {
		response.BadRequest(c, "refusing to delete every record without a filter; use DELETE /admin/usage/all")
		return
	}
	affected, err := model.DeleteUsageLogs(filter)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": affected})
}

func ClearUsage(c *gin.Context) {
	affected, err := model.ClearUsageLogs()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": affected})
}

func ListUsageModels(c *gin.Context) {
	list, err := model.ListUsageModels()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, list)
}

func UsageDaily(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "14"))
	rows, err := model.UsageDaily(days)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, rows)
}

func UsageByModel(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	rows, err := model.UsageByModel(limit)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, rows)
}

func UsageByAccount(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	rows, err := model.UsageByAccount(limit)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, rows)
}

func UsageOverview(c *gin.Context) {
	summary, err := model.UsageSummary()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	accounts, _ := model.ListAccounts()
	enabled := 0
	for _, acc := range accounts {
		if acc.Status == model.AccountStatusEnabled {
			enabled++
		}
	}
	summary["accounts_total"] = len(accounts)
	summary["accounts_enabled"] = enabled
	response.Success(c, summary)
}
