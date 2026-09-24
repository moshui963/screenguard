package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"screenguard/internal/types"
)

// JSONLWriter JSONL 事件流写入器 — 不可变审计原始流，只追加
type JSONLWriter struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewJSONLWriter 创建写入器，文件名按日期滚动
func NewJSONLWriter(dir string) (*JSONLWriter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建事件流目录: %w", err)
	}
	return &JSONLWriter{path: dir}, nil
}

// openToday 打开今天的 JSONL 文件
func (w *JSONLWriter) openToday() error {
	if w.file != nil {
		// 检查文件是否还是今天的
		fi, err := w.file.Stat()
		if err == nil && fi.ModTime().Format("2006-01-02") == time.Now().Format("2006-01-02") {
			return nil
		}
		w.file.Close()
	}
	name := filepath.Join(w.path, "events_"+time.Now().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("打开事件流文件: %w", err)
	}
	w.file = f
	return nil
}

// Append 追加一条事件到 JSONL 流
func (w *JSONLWriter) Append(e *types.DetectionEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.openToday(); err != nil {
		return err
	}

	// JSONL 比数据库表字段更全 — 含逐帧门控中间量
	record := map[string]interface{}{
		"id":              e.ID,
		"ts":              e.Ts.Format(time.RFC3339Nano),
		"session_id":      e.SessionID,
		"profile_id":      e.ProfileID,
		"model_id":        e.ModelID,
		"policy_version":  e.PolicyVersion,
		"frame_index":     e.FrameIndex,
		"screen_w":        e.ScreenW,
		"screen_h":        e.ScreenH,
		"capture_ms":      e.CaptureMs,
		"model_ms":        e.ModelMs,
		"total_ms":        e.TotalMs,
		"action":          string(e.Action),
		"result":          string(e.Result),
		"preempted_cause": e.PreemptedCause,
		"boxes":           e.Boxes,
		// 扩展字段（门控中间量）— replay 依赖这些
		"frame_consistency": map[string]interface{}{
			"n_required": nil, // 运行时填充
			"n_achieved": nil,
			"std_px":     nil,
			"centers":    nil, // 逐帧中心
		},
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("序列化事件: %w", err)
	}
	data = append(data, '\n')

	_, err = w.file.Write(data)
	return err
}

// AppendClick 记录点击动作
func (w *JSONLWriter) AppendClick(c *types.ClickAction) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.openToday(); err != nil {
		return err
	}
	record := map[string]interface{}{
		"type":          "click",
		"ts":            c.ExecutedAt.Format(time.RFC3339Nano),
		"event_id":      c.EventID,
		"point_px":      c.PointPx,
		"point_pt":      c.PointPt,
		"scale_factor":  c.ScaleFactor,
		"verify_result": string(c.VerifyResult),
		"retry_n":       c.RetryN,
		"latency_ms":    c.LatencyMs,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.file.Write(data)
	return err
}

// Close 关闭写入器
func (w *JSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// ReadEventsForReplay 读取指定时间窗内的事件用于模拟重放
func ReadEventsForReplay(dir string, from, to time.Time) ([]types.DetectionEvent, error) {
	var results []types.DetectionEvent
	// 遍历日期范围内的所有 JSONL 文件
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		name := filepath.Join(dir, "events_"+d.Format("2006-01-02")+".jsonl")
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		dec := json.NewDecoder(f)
		for {
			var record map[string]json.RawMessage
			if err := dec.Decode(&record); err != nil {
				break
			}
			// 过滤时间范围
			var tsStr string
			if v, ok := record["ts"]; ok {
				json.Unmarshal(v, &tsStr)
			}
			ts, err := time.Parse(time.RFC3339Nano, tsStr)
			if err != nil || ts.Before(from) || ts.After(to) {
				continue
			}
			// 解析事件（简化版）
			var e types.DetectionEvent
			if v, ok := record["action"]; ok {
				json.Unmarshal(v, &e.Action)
			}
			if v, ok := record["result"]; ok {
				json.Unmarshal(v, &e.Result)
			}
			e.Ts = ts
			results = append(results, e)
		}
		f.Close()
	}
	return results, nil
}
