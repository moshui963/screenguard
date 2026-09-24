package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"screenguard/internal/types"
)

// DB SQLite 数据库句柄
type DB struct {
	conn *sql.DB
}

// New 打开/创建 SQLite 数据库
func New(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}

	// DSN 中的 _pragma 与原 mattn 驱动的 _journal_mode/_busy_timeout/_foreign_keys
	// 完全等价，且驱动在每条新建连接上都会执行，因此 WAL、忙等待超时、
	// 外键约束的行为与原先保持一致。
	conn, err := sql.Open(sqliteDriverName,
		dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("数据库迁移: %w", err)
	}
	return db, nil
}

// Close 关闭数据库
func (db *DB) Close() error {
	return db.conn.Close()
}

// migrate 执行全部建表语句，与 PRD 5.1 节表结构一致
func (db *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS device_profile (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			fingerprint     TEXT UNIQUE NOT NULL,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			imgsz           INTEGER NOT NULL,
			capture_backend TEXT NOT NULL,
			capture_ok      INTEGER NOT NULL DEFAULT 0,
			per_class       TEXT DEFAULT '{}',
			bias_px        REAL NOT NULL DEFAULT 0,
			std_px         REAL NOT NULL DEFAULT 0,
			capture_ms      INTEGER NOT NULL DEFAULT 0,
			model_ms        INTEGER NOT NULL DEFAULT 0,
			model_sha256    TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'active'
		)`,
		`CREATE TABLE IF NOT EXISTS model_version (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT NOT NULL,
			file_path  TEXT NOT NULL,
			sha256     TEXT UNIQUE NOT NULL,
			arch       TEXT NOT NULL DEFAULT '',
			params     REAL NOT NULL DEFAULT 0,
			train_imgsz INTEGER NOT NULL DEFAULT 640,
			classes    TEXT DEFAULT '[]',
			metrics    TEXT DEFAULT '{}',
			enabled    INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS policy_version (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			version      TEXT UNIQUE NOT NULL,
			yaml_hash    TEXT NOT NULL,
			content      TEXT NOT NULL,
			published_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			author       TEXT NOT NULL DEFAULT '',
			note         TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS app_target (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			bundle_id    TEXT UNIQUE NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			first_seen   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			times_seen   INTEGER NOT NULL DEFAULT 0,
			times_clicked INTEGER NOT NULL DEFAULT 0,
			times_failed  INTEGER NOT NULL DEFAULT 0,
			trust_score   REAL NOT NULL DEFAULT 0.5,
			mode_override TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS detection_event (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			ts              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			session_id      TEXT NOT NULL,
			profile_id      INTEGER REFERENCES device_profile(id),
			model_id        INTEGER REFERENCES model_version(id),
			policy_version  TEXT NOT NULL,
			frame_index     INTEGER NOT NULL DEFAULT 0,
			screen_w        INTEGER NOT NULL,
			screen_h        INTEGER NOT NULL,
			capture_ms      INTEGER NOT NULL DEFAULT 0,
			model_ms        INTEGER NOT NULL DEFAULT 0,
			total_ms        INTEGER NOT NULL DEFAULT 0,
			action          TEXT NOT NULL,
			result          TEXT NOT NULL DEFAULT 'none',
			preempted_cause TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS detection_box (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id      INTEGER NOT NULL REFERENCES detection_event(id) ON DELETE CASCADE,
			cls           INTEGER NOT NULL,
			conf          REAL NOT NULL,
			xyxy          TEXT NOT NULL,
			role          TEXT NOT NULL,
			gate_passed    INTEGER NOT NULL DEFAULT 0,
			reject_reason TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS click_action (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id      INTEGER NOT NULL UNIQUE REFERENCES detection_event(id) ON DELETE CASCADE,
			point_px      TEXT NOT NULL,
			point_pt      TEXT NOT NULL,
			scale_factor  REAL NOT NULL DEFAULT 1.0,
			executed_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			verify_result TEXT NOT NULL DEFAULT 'none',
			retry_n       INTEGER NOT NULL DEFAULT 0,
			latency_ms    INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS review_item (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id        INTEGER REFERENCES detection_event(id) ON DELETE SET NULL,
			reason          TEXT NOT NULL,
			snapshot_path   TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'pending',
			label_correction TEXT DEFAULT '{}',
			resolved_at     DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS dataset_item (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			review_item_id INTEGER REFERENCES review_item(id) ON DELETE SET NULL,
			image_path     TEXT NOT NULL,
			label_path     TEXT NOT NULL DEFAULT '',
			state          TEXT NOT NULL DEFAULT 'raw',
			batch_id       TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS blacklist (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			feature_type TEXT NOT NULL,
			feature_value TEXT NOT NULL,
			reason       TEXT NOT NULL DEFAULT '',
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at   DATETIME,
			source       TEXT NOT NULL DEFAULT 'auto'
		)`,
		`CREATE TABLE IF NOT EXISTS stats_daily (
			date            TEXT PRIMARY KEY,
			popups_seen     INTEGER NOT NULL DEFAULT 0,
			auto_closed     INTEGER NOT NULL DEFAULT 0,
			false_positives INTEGER NOT NULL DEFAULT 0,
			avg_latency_ms  INTEGER NOT NULL DEFAULT 0,
			top_apps        TEXT DEFAULT '[]',
			crash_count     INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS runtime_log (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			ts      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			level   TEXT NOT NULL,
			kind    TEXT NOT NULL,
			message TEXT NOT NULL,
			stack   TEXT NOT NULL DEFAULT ''
		)`,
		// 索引
		`CREATE INDEX IF NOT EXISTS idx_event_ts ON detection_event(ts)`,
		`CREATE INDEX IF NOT EXISTS idx_event_action ON detection_event(action)`,
		`CREATE INDEX IF NOT EXISTS idx_box_event ON detection_box(event_id)`,
		`CREATE INDEX IF NOT EXISTS idx_review_status ON review_item(status)`,
		`CREATE INDEX IF NOT EXISTS idx_blacklist_expires ON blacklist(expires_at)`,
	}
	for _, s := range stmts {
		if _, err := db.conn.Exec(s); err != nil {
			return fmt.Errorf("执行建表: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

// ============================================================
// detection_event CRUD
// ============================================================

// InsertEvent 插入检测事件，返回自增 ID
func (db *DB) InsertEvent(e *types.DetectionEvent) (int64, error) {
	// profile_id / model_id 是外键（分别指向 device_profile / model_version）。
	// 事件构造时若未关联具体档位/模型，字段为零值 0，而 SQLite 自增 id 从 1 开始，
	// 直接写 0 会触发 FOREIGN KEY constraint failed。
	// "未关联" 的正确表达是 NULL（SQLite 允许 NULL 外键），故此处做零值 → NULL 转换。
	var profileID, modelID any
	if e.ProfileID > 0 {
		profileID = e.ProfileID
	}
	if e.ModelID > 0 {
		modelID = e.ModelID
	}

	res, err := db.conn.Exec(
		`INSERT INTO detection_event (ts, session_id, profile_id, model_id, policy_version,
			frame_index, screen_w, screen_h, capture_ms, model_ms, total_ms,
			action, result, preempted_cause)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.Ts, e.SessionID, profileID, modelID, e.PolicyVersion,
		e.FrameIndex, e.ScreenW, e.ScreenH, e.CaptureMs, e.ModelMs, e.TotalMs,
		string(e.Action), string(e.Result), e.PreemptedCause,
	)
	if err != nil {
		return 0, fmt.Errorf("插入事件: %w", err)
	}
	id, _ := res.LastInsertId()
	e.ID = id

	// 插入所有检测框
	for i := range e.Boxes {
		b := &e.Boxes[i]
		xyxy := fmt.Sprintf("[%.1f,%.1f,%.1f,%.1f]", b.Box[0], b.Box[1], b.Box[2], b.Box[3])
		_, err := db.conn.Exec(
			`INSERT INTO detection_box (event_id, cls, conf, xyxy, role, gate_passed, reject_reason)
			 VALUES (?,?,?,?,?,?,?)`,
			id, b.ClS, b.Conf, xyxy, string(b.Role), b.GatePassed, b.RejectReason,
		)
		if err != nil {
			return id, fmt.Errorf("插入检测框: %w", err)
		}
	}
	return id, nil
}

// InsertClickAction 插入点击记录
func (db *DB) InsertClickAction(c *types.ClickAction) error {
	_, err := db.conn.Exec(
		`INSERT INTO click_action (event_id, point_px, point_pt, scale_factor, executed_at,
			verify_result, retry_n, latency_ms)
		 VALUES (?,?,?,?,?,?,?,?)`,
		c.EventID,
		fmt.Sprintf("[%.2f,%.2f]", c.PointPx[0], c.PointPx[1]),
		fmt.Sprintf("[%.2f,%.2f]", c.PointPt[0], c.PointPt[1]),
		c.ScaleFactor, c.ExecutedAt, string(c.VerifyResult), c.RetryN, c.LatencyMs,
	)
	return err
}

// InsertProfile 插入设备自检结果
func (db *DB) InsertProfile(p *types.DeviceProfile) (int64, error) {
	// 先把旧的 active 转为历史
	_, _ = db.conn.Exec(`UPDATE device_profile SET status='history' WHERE fingerprint=?`, p.Fingerprint)
	res, err := db.conn.Exec(
		`INSERT INTO device_profile (fingerprint, imgsz, capture_backend, capture_ok, per_class,
			bias_px, std_px, capture_ms, model_ms, model_sha256, status)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.Fingerprint, p.Imgsz, p.CaptureBackend, p.CaptureOK,
		"", // per_class JSON — 简化版，后续可序列化
		p.BiasPx, p.StdPx, p.CaptureMs, p.ModelMs, p.ModelSha256, "active",
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// GetActiveProfile 获取当前设备的活跃 profile
func (db *DB) GetActiveProfile(fingerprint string) (*types.DeviceProfile, error) {
	var p types.DeviceProfile
	err := db.conn.QueryRow(
		`SELECT id, fingerprint, created_at, imgsz, capture_backend, capture_ok,
			bias_px, std_px, capture_ms, model_ms, model_sha256, status
		 FROM device_profile WHERE fingerprint=? AND status='active'`, fingerprint,
	).Scan(&p.ID, &p.Fingerprint, &p.CreatedAt, &p.Imgsz, &p.CaptureBackend, &p.CaptureOK,
		&p.BiasPx, &p.StdPx, &p.CaptureMs, &p.ModelMs, &p.ModelSha256, &p.Status)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// InsertRuntimeLog 插入运行时日志
func (db *DB) InsertRuntimeLog(level, kind, message, stack string) error {
	_, err := db.conn.Exec(
		`INSERT INTO runtime_log (level, kind, message, stack) VALUES (?,?,?,?)`,
		level, kind, message, stack)
	return err
}

// CleanupOldEvents 按保留天数清理过期事件（含级联删除 detection_box 和 click_action）
func (db *DB) CleanupOldEvents(retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	_, err := db.conn.Exec(
		`DELETE FROM detection_event WHERE ts < ?`, cutoff)
	return err
}

// GetEventByID 按 ID 查询事件（含检测框）
func (db *DB) GetEventByID(id int64) (*types.DetectionEvent, error) {
	var e types.DetectionEvent
	err := db.conn.QueryRow(
		`SELECT id, ts, session_id, profile_id, model_id, policy_version, frame_index,
			screen_w, screen_h, capture_ms, model_ms, total_ms, action, result, preempted_cause
		 FROM detection_event WHERE id=?`, id,
	).Scan(&e.ID, &e.Ts, &e.SessionID, &e.ProfileID, &e.ModelID, &e.PolicyVersion,
		&e.FrameIndex, &e.ScreenW, &e.ScreenH, &e.CaptureMs, &e.ModelMs, &e.TotalMs,
		&e.Action, &e.Result, &e.PreemptedCause)
	if err != nil {
		return nil, err
	}
	// 加载检测框
	rows, err := db.conn.Query(
		`SELECT cls, conf, xyxy, role, gate_passed, reject_reason
		 FROM detection_box WHERE event_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b types.DetectionBox
		var xyxyStr, roleStr string
		if err := rows.Scan(&b.ClS, &b.Conf, &xyxyStr, &roleStr, &b.GatePassed, &b.RejectReason); err != nil {
			continue
		}
		b.Role = types.BoxRole(roleStr)
		e.Boxes = append(e.Boxes, b)
	}
	return &e, nil
}

// QueryEvents 查询事件列表（时间倒序，可按 action / cls 过滤）
// filter: "" = 全部; "action:click" / "cls:GuanBi"（cls 支持类别名，也兼容类别下标）
//
// 返回字段做过"可读性增强"——除事件本体外还带上本次处置的：
//   - 主目标框（primary）：类别名、置信度、屏幕中心点、框尺寸、是否过闸、拒因
//   - 容器框（container）：弹窗本体的类别与包围盒
//   - 真实落点（click_action）：点击坐标、复核结果、重试次数
// 事件流因此能直接显示"点了什么、点在哪、为什么跳过"，而不是一串英文枚举。
func (db *DB) QueryEvents(limit, offset int, filter string) ([]map[string]any, error) {
	where := "1=1"
	args := []any{}
	if len(filter) > 4 && filter[:4] == "cls:" {
		where = `EXISTS (SELECT 1 FROM detection_box b WHERE b.event_id = e.id AND b.cls = ?)`
		args = append(args, classIndex(filter[4:]))
	} else if len(filter) > 7 && filter[:7] == "action:" {
		where = "e.action = ?"
		args = append(args, filter[7:])
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	// pb = 主目标框：优先"已过闸的出口"，其次任意出口，再次置信度最高的框
	// cb = 容器框（role='container'）；ca = 真实点击记录
	q := `SELECT e.id, e.ts, e.action, e.result, e.total_ms, e.policy_version,
			e.screen_w, e.screen_h, e.capture_ms, e.model_ms, e.frame_index, e.preempted_cause,
			(SELECT COUNT(*) FROM detection_box b WHERE b.event_id = e.id) AS box_count,
			pb.cls, pb.conf, pb.xyxy, pb.role, pb.gate_passed, pb.reject_reason,
			cb.cls, cb.conf, cb.xyxy,
			ca.point_px, ca.verify_result, ca.retry_n, ca.latency_ms
	      FROM detection_event e
	      LEFT JOIN detection_box pb ON pb.id = (
			SELECT id FROM detection_box b2 WHERE b2.event_id = e.id
			ORDER BY (b2.role='exit') DESC, b2.gate_passed DESC, b2.conf DESC LIMIT 1)
	      LEFT JOIN detection_box cb ON cb.id = (
			SELECT id FROM detection_box b3 WHERE b3.event_id = e.id AND b3.role='container'
			ORDER BY b3.conf DESC LIMIT 1)
	      LEFT JOIN click_action ca ON ca.event_id = e.id
	      WHERE ` + where + `
	      ORDER BY e.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var id, boxCount, totalMs, captureMs, modelMs, frameIdx, screenW, screenH int64
		var ts, action, result, policyVersion, preemptedCause string
		var pCls, pConf, pXYXY, pRole, pReason, cCls, cConf, cXYXY sql.NullString
		var pGate sql.NullInt64
		var clickPx, clickVerify sql.NullString
		var clickRetry, clickLatency sql.NullInt64

		err := rows.Scan(&id, &ts, &action, &result, &totalMs, &policyVersion,
			&screenW, &screenH, &captureMs, &modelMs, &frameIdx, &preemptedCause,
			&boxCount,
			&pCls, &pConf, &pXYXY, &pRole, &pGate, &pReason,
			&cCls, &cConf, &cXYXY,
			&clickPx, &clickVerify, &clickRetry, &clickLatency)
		if err != nil {
			continue
		}

		row := map[string]any{
			"id": id, "ts": ts, "action": action, "result": result,
			"total_ms": totalMs, "policy_version": policyVersion, "box_count": boxCount,
			"screen_w": screenW, "screen_h": screenH,
			"capture_ms": captureMs, "model_ms": modelMs, "frame_index": frameIdx,
			"preempted_cause": preemptedCause,
		}

		// ---- 主目标框（"点了什么 / 想点什么"）----
		if pXYXY.Valid {
			xyxy := parseFloatList(pXYXY.String)
			if len(xyxy) == 4 {
				row["cls"] = clsIndexOf(pCls)
				row["cls_name"] = classNameOf(clsIndexOf(pCls))
				row["conf"] = atofOr(pConf, 0)
				row["role"] = pRole.String
				row["gate_passed"] = pGate.Int64 != 0
				if pReason.Valid {
					row["reject_reason"] = pReason.String
				}
				row["x1"] = xyxy[0]
				row["y1"] = xyxy[1]
				row["x2"] = xyxy[2]
				row["y2"] = xyxy[3]
				row["cx"] = (xyxy[0] + xyxy[2]) / 2
				row["cy"] = (xyxy[1] + xyxy[3]) / 2
				row["w"] = xyxy[2] - xyxy[0]
				row["h"] = xyxy[3] - xyxy[1]
			}
		}
		// ---- 事件级拒因（如 no_exit：弹窗在但未检出关闭按钮）----
		// 框级拒因为空且动作为上报时，用 preempted_cause 充当 reject_reason，
		// 让事件流能解释"为什么没点"。
		if action == "report_only" && (row["reject_reason"] == nil || row["reject_reason"] == "") && preemptedCause != "" {
			row["reject_reason"] = preemptedCause
		}

		// ---- 容器框（弹窗本体）----
		if cXYXY.Valid {
			cxy := parseFloatList(cXYXY.String)
			if len(cxy) == 4 {
				row["container_cls"] = classNameOf(clsIndexOf(cCls))
				row["container_x1"] = cxy[0]
				row["container_y1"] = cxy[1]
				row["container_x2"] = cxy[2]
				row["container_y2"] = cxy[3]
				row["container_conf"] = atofOr(cConf, 0)
			}
		}
		// ---- 真实落点 ----
		if clickPx.Valid {
			if pt := parseFloatList(clickPx.String); len(pt) == 2 {
				row["click_x"] = pt[0]
				row["click_y"] = pt[1]
			}
		}
		if clickVerify.Valid {
			row["click_verify"] = clickVerify.String
			row["click_retry"] = clickRetry.Int64
			row["click_latency_ms"] = clickLatency.Int64
		}

		out = append(out, row)
	}
	return out, nil
}

// classNameOf 把类别下标映射到类别名（越界时给出可读占位，避免前端显示空白）
func classNameOf(idx int) string {
	if idx >= 0 && idx < len(types.ClassNames) {
		return types.ClassNames[idx]
	}
	return fmt.Sprintf("cls#%d", idx)
}

// clsIndexOf 解析数据库里存的类别下标（存的是 cls 整数）
func clsIndexOf(v sql.NullString) int {
	if v.Valid {
		if n, err := strconv.Atoi(strings.TrimSpace(v.String)); err == nil {
			return n
		}
	}
	return -1
}

// classIndex 把过滤条件里的类别名（或下标字符串）转成下标，未识别返回 -1
func classIndex(key string) int {
	key = strings.TrimSpace(key)
	if key == "" {
		return -1
	}
	if n, err := strconv.Atoi(key); err == nil {
		return n
	}
	for i, n := range types.ClassNames {
		if n == key {
			return i
		}
	}
	return -1
}

// parseFloatList 解析 "[1.0,2.0,3.0,4.0]" / "[1.0,2.0]" 形式的字符串
func parseFloatList(s string) []float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		if v, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func atofOr(v sql.NullString, def float64) float64 {
	if v.Valid {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v.String), 64); err == nil {
			return f
		}
	}
	return def
}
