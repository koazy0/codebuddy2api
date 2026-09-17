package admin

import (
	"strconv"
	"strings"

	"codebuddy-gateway/api/response"
	"codebuddy-gateway/model"
	"codebuddy-gateway/service"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// 任务中心（成长中心积分任务）
//
// 接口对齐上游状态机的三个动作：
//   - 查列表：看每个任务的进度 / 奖励 / 状态
//   - 一键完成：accept → 上报行为事件 → 等计分 → 自动领奖
//   - 手动领取：对已达标任务单独领奖
//
// 全部幂等：已领取的任务不会重复加分，重复调用安全。
// ---------------------------------------------------------------------------

// taskClient 拿到上游客户端单例。
func taskClient() *service.UpstreamClient {
	return service.DefaultClient
}

// TaskList 列出指定账号的成长任务。
func TaskList(c *gin.Context) {
	acc, ok := lookupAccount(c)
	if !ok {
		return
	}
	tasks, err := taskClient().GrowthListTasks(c.Request.Context(), acc)
	if err != nil {
		response.Fail(c, "拉取任务列表失败: "+err.Error())
		return
	}
	service.SortTasksByReward(tasks)
	response.Success(c, gin.H{
		"tasks":          tasks,
		"pending_credit": pendingOf(tasks),
	})
}

// TaskCatalogList 返回可一键完成的任务清单（排障用：查哪些任务码可自动化）。
func TaskCatalogList(c *gin.Context) {
	response.Success(c, gin.H{"catalog": service.TaskCatalog()})
}

type taskRunReq struct {
	// Codes 为空时跑全部可自动化任务。
	Codes []string `json:"codes"`
}

// TaskRun 一键完成：跑完动作链并自动领奖。
func TaskRun(c *gin.Context) {
	acc, ok := lookupAccount(c)
	if !ok {
		return
	}
	var req taskRunReq
	// body 可为空（跑全部），解析失败不报错。
	_ = c.ShouldBindJSON(&req)
	summary := taskClient().RunAccountTasks(c.Request.Context(), acc, req.Codes)
	response.Success(c, summary)
}

// TaskClaim 单独领取某个已达标任务的奖励。
func TaskClaim(c *gin.Context) {
	acc, ok := lookupAccount(c)
	if !ok {
		return
	}
	code := strings.TrimSpace(c.Param("code"))
	if code == "" {
		response.Fail(c, "缺少任务码")
		return
	}
	credit, energy, err := taskClient().GrowthClaimReward(c.Request.Context(), acc, code)
	if err != nil {
		response.Fail(c, "领取失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{
		"task_code": code, "credit": credit, "energy": energy,
		"already_claimed": credit == 0 && energy == 0,
	})
}

// TaskAcceptAll 批量报名全部未接受的任务。
func TaskAcceptAll(c *gin.Context) {
	acc, ok := lookupAccount(c)
	if !ok {
		return
	}
	tasks, err := taskClient().GrowthListTasks(c.Request.Context(), acc)
	if err != nil {
		response.Fail(c, "拉取任务列表失败: "+err.Error())
		return
	}
	var codes []string
	for _, t := range tasks {
		if t.Claimed || t.Locked {
			continue
		}
		if t.AcceptStatus == "accepted" || t.AcceptStatus == "completed" {
			continue
		}
		codes = append(codes, t.TaskCode)
	}
	if len(codes) == 0 {
		response.Success(c, gin.H{"accepted": 0, "message": "没有待接受的任务"})
		return
	}
	if err := taskClient().GrowthAcceptTasks(c.Request.Context(), acc, codes); err != nil {
		response.Fail(c, "报名失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"accepted": len(codes), "codes": codes})
}

// lookupAccount 从路径参数取账号并校验存在。
func lookupAccount(c *gin.Context) (*model.Account, bool) {
	idRaw := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseUint(idRaw, 10, 64)
	if err != nil || id == 0 {
		response.Fail(c, "无效的账号 ID")
		return nil, false
	}
	acc, err := model.GetAccountByID(uint(id))
	if err != nil || acc == nil {
		response.Fail(c, "账号不存在")
		return nil, false
	}
	return acc, true
}

// pendingOf 汇总未领取任务的奖励总额。
func pendingOf(tasks []service.GrowthTask) int64 {
	var total int64
	for _, t := range tasks {
		if t.Claimed {
			continue
		}
		total += t.Credit
	}
	return total
}

// ---------------------------------------------------------------------------
// 批量执行
//
// 设计要点：接口只启动、立刻返回，实际执行在后台跑，前端轮询进度。
// 原因是单账号要 1-2 分钟，多账号串在一个 HTTP 请求里必然被
// 反向代理或隧道（cloudflared 等）按空闲超时掐断。
// ---------------------------------------------------------------------------

type taskBatchReq struct {
	// Codes 为空时跑全部可自动化任务。
	Codes []string `json:"codes"`
	// Concurrency 并发账号数，缺省 3，上限 8。
	Concurrency int `json:"concurrency"`
}

// TaskBatchStart 启动一批账号的任务执行。
func TaskBatchStart(c *gin.Context) {
	var req taskBatchReq
	_ = c.ShouldBindJSON(&req)
	state, err := service.StartBatchTasks(req.Codes, req.Concurrency)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, state)
}

// TaskBatchStatus 查询当前批量执行的进度。
func TaskBatchStatus(c *gin.Context) {
	state := service.BatchTasksStatus()
	if state == nil {
		response.Success(c, gin.H{"none": true})
		return
	}
	response.Success(c, state)
}

// CostLedger 返回实测成本账本的规模与分层结果，供排障判断选号分层是否合理。
//
// 为什么需要这个端点：tier 硬过滤是黑盒行为——线上如果发现
// 「某个模型总是只用少数几个号」，得能看出是免费号垄断还是分层出错了。
func CostLedger(c *gin.Context) {
	total, free, paid := service.CostLedgerStats()
	response.Success(c, gin.H{
		"total": total, "free": free, "paid": paid,
		"ttl_minutes": 360,
	})
}
