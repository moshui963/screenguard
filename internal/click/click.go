package click

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrSelfWindow 自激防护拦截：点击目标落在应用自身窗口内，点击被拒绝执行。
// 由 Windows clicker 在发送输入前判定，执行器见此错误立即中止（不重试、不记为失败）。
var ErrSelfWindow = errors.New("点击目标落在自身窗口内，已自激防护拦截")

// ============================================================
// 点击执行层 — PRD 4.3.3 消失验证
// 点击是全局资源，执行层全局单锁串行
// ============================================================

// Clicker 点击接口
type Clicker interface {
	// Click 在指定物理像素坐标执行点击
	Click(x, y float64, scaleFactor float64) error
	// MoveTo 仅移动光标到指定物理像素坐标（不点击），用于自检/调试验证坐标换算
	MoveTo(x, y float64, scaleFactor float64)
	// VerifyVanish 真实消失验证（红线 B6）：
	// 点击后延迟 300ms 复截，与点击前帧做 ROI 帧差
	//   vanished=true  目标区域不再有显著差异（弹窗已消失）
	//   vanished=false 区域内容基本没变（点击没生效）
	VerifyVanish(before interface{}) (vanished bool, err error)
}

// Executor 点击执行器 — 全局单锁串行
type Executor struct {
	mu      sync.Mutex
	clicker Clicker
}

// NewExecutor 创建执行器
func NewExecutor(c Clicker) *Executor {
	return &Executor{clicker: c}
}

// ExecuteClick 执行一次完整点击流程（含真实验证）
// before: 点击前捕获的帧（用于消失验证对比），可为 nil（此时验证返回 false）
// PRD 4.3.3:
//
//	点击 → 延迟 300ms 复截 → ROI 帧差判定
//	未消失重试: ①中心微调重试 ②再次 ③放弃并 failed（上限 3 次）
func (e *Executor) ExecuteClick(pointPx [2]float64, scaleFactor float64, before interface{}) (result string, retryN int, latencyMs int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	maxRetry := 3
	start := time.Now()

	for retry := 0; retry < maxRetry; retry++ {
		if err := e.clicker.Click(pointPx[0], pointPx[1], scaleFactor); err != nil {
			if errors.Is(err, ErrSelfWindow) {
				// 自激防护拦截：目标是自身窗口，不重试、不记为失败
				return "self", retry, int(time.Since(start).Milliseconds())
			}
			return "failed", retry, int(time.Since(start).Milliseconds())
		}

		// 延迟 300ms 后验证（PRD 4.3.3）
		time.Sleep(300 * time.Millisecond)

		if before == nil {
			// 没有前帧无法验证 → 按未消失处理，走重试
			continue
		}

		vanished, err := e.clicker.VerifyVanish(before)
		if err != nil {
			return "failed", retry, int(time.Since(start).Milliseconds())
		}
		if vanished {
			return "vanished", retry, int(time.Since(start).Milliseconds())
		}

		// 未消失 → 重试时中心微调（点击热区常大于可见图标）
		if retry == 0 {
			pointPx[0] += 2
			pointPx[1] += 1
		}
	}

	return "failed", maxRetry - 1, int(time.Since(start).Milliseconds())
}

// FormatClickResult 格式化点击结果
func FormatClickResult(result string, retryN, latencyMs int) string {
	return fmt.Sprintf("result=%s retries=%d latency=%dms", result, retryN, latencyMs)
}
