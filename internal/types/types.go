package types

import "time"

// ============================================================
// 全局枚举常量 — 与 PRD 5.2 节枚举值说明严格一致
// ============================================================

// AppMode 软件运行模式
type AppMode string

const (
	ModeObserve         AppMode = "observe"
	ModeAuto            AppMode = "auto"
	ModeExtreme         AppMode = "extreme"
	ModePaused          AppMode = "paused"
	ModeDegraded        AppMode = "degraded"
	ModeUnauthorized    AppMode = "unauthorized"
	ModeSelftestPending AppMode = "selftest_pending"
	ModeSelftestFailed  AppMode = "selftest_failed"
)

// PopupState 弹窗处置状态机
type PopupState string

const (
	PopupIDLE      PopupState = "IDLE"
	PopupSUSPECT   PopupState = "SUSPECT"
	PopupSTABLE    PopupState = "STABLE"
	PopupARMED     PopupState = "ARMED"
	PopupCLICKING  PopupState = "CLICKING"
	PopupVERIFYING PopupState = "VERIFYING"
	PopupCLOSED    PopupState = "CLOSED"
	PopupRETRY     PopupState = "RETRY"
	PopupFAILED    PopupState = "FAILED"
	PopupCOOLDOWN  PopupState = "COOLDOWN"
)

// Action 决策动作
type Action string

const (
	ActionClick                   Action = "click"
	ActionReportOnly              Action = "report_only"
	ActionBlockedOutsideContainer Action = "blocked_outside_container"
	ActionBlockedDup              Action = "blocked_dup"
	ActionBlockedIllegalArea      Action = "blocked_illegal_area"
	ActionLowConf                 Action = "low_conf"
	ActionDisabledClass           Action = "disabled_class"
	ActionSuppressedRapid         Action = "suppressed_rapid"
	ActionBlockedUserActive       Action = "blocked_user_active"
	ActionBlockedForbiddenZone    Action = "blocked_forbidden_zone"
	ActionBudgetExceeded          Action = "budget_exceeded"
	ActionGaveUpTimeout           Action = "gave_up_timeout"
)

// Result 执行结果
type Result string

const (
	ResultNone      Result = "none"
	ResultVanished  Result = "vanished"
	ResultRetry     Result = "retry"
	ResultFailed    Result = "failed"
	ResultPreempted Result = "preempted"
)

// BoxRole 框角色
type BoxRole string

const (
	RoleContainer BoxRole = "container"
	RoleExit      BoxRole = "exit"
)

// PolicyMode 策略模式
type PolicyMode string

const (
	PolicyClick    PolicyMode = "click"
	PolicyReport   PolicyMode = "report"
	PolicyDisabled PolicyMode = "disabled"
)

// PerformanceTier 性能档位
type PerformanceTier string

const (
	PerfPowersave  PerformanceTier = "powersave"
	PerfBalanced   PerformanceTier = "balanced"
	PerfAggressive PerformanceTier = "aggressive"
)

// ReviewReason 复核原因
type ReviewReason string

const (
	ReviewFailedClick          ReviewReason = "failed_click"
	ReviewUserNotPopup         ReviewReason = "user_not_popup"
	ReviewUserWrongTarget      ReviewReason = "user_wrong_target"
	ReviewLowConfHighPotential ReviewReason = "low_conf_high_potential"
	ReviewNoExitFound          ReviewReason = "no_exit_found"
)

// ReviewStatus 复核状态
type ReviewStatus string

const (
	ReviewStatusPending  ReviewStatus = "pending"
	ReviewStatusLabelled ReviewStatus = "labeled"
	ReviewStatusIgnored  ReviewStatus = "ignored"
	ReviewStatusExported ReviewStatus = "exported"
)

// DatasetItemState 数据集条目状态
type DatasetItemState string

const (
	DSRaw      DatasetItemState = "raw"
	DSLabeled  DatasetItemState = "labeled"
	DSExported DatasetItemState = "exported"
)

// BlacklistFeatureType 黑名单特征类型
type BlacklistFeatureType string

const (
	BLBundleId           BlacklistFeatureType = "bundle_id"
	BLTitleRegex         BlacklistFeatureType = "title_regex"
	BLContainerSizeRange BlacklistFeatureType = "container_size_range"
)

// ============================================================
// 检测类别 — 10 类（当前已训练）
// ============================================================

// ClassNames YOLO 模型的 10 个类别名
var ClassNames = []string{
	"TanChuang", "GuanBi", "GuanBi_str", "XiaYiBu", "QuXiao",
	"TuiChu", "TiaoGuo", "WoBuShou", "ZhiDaoLe", "YunXu",
}

// ContainerClass 容器类别名
const ContainerClass = "TanChuang"

// ExitClasses 所有出口类别（非容器）
var ExitClasses = []string{
	"GuanBi", "GuanBi_str", "XiaYiBu", "QuXiao", "TuiChu",
	"TiaoGuo", "WoBuShou", "ZhiDaoLe", "YunXu",
}

// ============================================================
// 核心数据结构
// ============================================================

// BoxXYXY 边界框 [x1, y1, x2, y2] — 物理像素坐标
type BoxXYXY [4]float64

// Center 框中心点
type Center [2]float64

// DetectionBox 单个检测框
type DetectionBox struct {
	ClS          int     `json:"cls"`
	ClSName      string  `json:"cls_name"`
	Conf         float64 `json:"conf"`
	Box          BoxXYXY `json:"box"`
	Center       Center  `json:"center"`
	Role         BoxRole `json:"role"`
	GatePassed   bool    `json:"gate_passed"`
	RejectReason string  `json:"reject_reason,omitempty"`
}

// DetectionEvent 一次完整决策事件 — 对应 detection_event 表
type DetectionEvent struct {
	ID             int64          `json:"id"`
	Ts             time.Time      `json:"ts"`
	SessionID      string         `json:"session_id"`
	ProfileID      int64          `json:"profile_id"`
	ModelID        int64          `json:"model_id"`
	PolicyVersion  string         `json:"policy_version"`
	FrameIndex     int64          `json:"frame_index"`
	ScreenW        int            `json:"screen_w"`
	ScreenH        int            `json:"screen_h"`
	CaptureMs      int            `json:"capture_ms"`
	ModelMs        int            `json:"model_ms"`
	TotalMs        int            `json:"total_ms"`
	Action         Action         `json:"action"`
	Result         Result         `json:"result"`
	PreemptedCause string         `json:"preempted_cause,omitempty"`
	Boxes          []DetectionBox `json:"boxes"`
}

// ClickAction 真实点击记录
type ClickAction struct {
	ID           int64      `json:"id"`
	EventID      int64      `json:"event_id"`
	PointPx      [2]float64 `json:"point_px"`
	PointPt      [2]float64 `json:"point_pt"`
	ScaleFactor  float64    `json:"scale_factor"`
	ExecutedAt   time.Time  `json:"executed_at"`
	VerifyResult Result     `json:"verify_result"`
	RetryN       int        `json:"retry_n"`
	LatencyMs    int        `json:"latency_ms"`
}

// DeviceProfile 设备自检产物
type DeviceProfile struct {
	ID             int64          `json:"id"`
	Fingerprint    string         `json:"fingerprint"`
	CreatedAt      time.Time      `json:"created_at"`
	Imgsz          int            `json:"imgsz"`
	CaptureBackend string         `json:"capture_backend"`
	CaptureOK      bool           `json:"capture_ok"`
	PerClass       []ClassProfile `json:"per_class"`
	BiasPx         float64        `json:"bias_px"`
	StdPx          float64        `json:"std_px"`
	CaptureMs      int            `json:"capture_ms"`
	ModelMs        int            `json:"model_ms"`
	ModelSha256    string         `json:"model_sha256"`
	Status         string         `json:"status"`
}

// ClassProfile 逐类自检结果
type ClassProfile struct {
	ClassName string     `json:"class_name"`
	Mode      PolicyMode `json:"mode"`
	BiasPx    float64    `json:"bias_px"`
	Recall    float64    `json:"recall"`
}

// ModelVersion 模型版本
type ModelVersion struct {
	ID         int64                  `json:"id"`
	Name       string                 `json:"name"`
	FilePath   string                 `json:"file_path"`
	SHA256     string                 `json:"sha256"`
	Arch       string                 `json:"arch"`
	Params     float64                `json:"params"`
	TrainImgsz int                    `json:"train_imgsz"`
	Classes    []string               `json:"classes"`
	Metrics    map[string]interface{} `json:"metrics"`
	Enabled    bool                   `json:"enabled"`
	CreatedAt  time.Time              `json:"created_at"`
}

// PolicyVersion 策略版本
type PolicyVersion struct {
	ID          int64     `json:"id"`
	Version     string    `json:"version"`
	YAMLHash    string    `json:"yaml_hash"`
	Content     string    `json:"content"`
	PublishedAt time.Time `json:"published_at"`
	Author      string    `json:"author"`
	Note        string    `json:"note"`
}

// AppConfig 应用配置（config.json）
type AppConfig struct {
	Maintainer           bool            `json:"maintainer"`
	Mode                 AppMode         `json:"mode"`
	PerformanceTier      PerformanceTier `json:"performance_tier"`
	ImgszOverride        int             `json:"imgsz_override"`
	InferThreads         int             `json:"infer_threads"`
	FpsLimit             float64         `json:"fps_limit"`
	SaveScreenshots      bool            `json:"save_screenshots"`
	ScreenshotDays       int             `json:"screenshot_days"`
	ScreenshotPath       string          `json:"screenshot_path"`
	MouseFollow          bool            `json:"mouse_follow"`
	BlurSensitive        bool            `json:"blur_sensitive"`
	UploadStructured     bool            `json:"upload_structured"`
	LogRetentionDays     int             `json:"log_retention_days"`
	LogDir               string          `json:"log_dir"`
	ModelPath            string          `json:"model_path"`
	PolicyPath           string          `json:"policy_path"`
	SharedPolicyURL      string          `json:"shared_policy_url"`
	PolicyPollSec        int             `json:"policy_poll_sec"`
	NegFrameCapture      bool            `json:"neg_frame_capture"`
	NegFrameInterval     int             `json:"neg_frame_interval"`
	OverlayEnabled       bool            `json:"overlay_enabled"`
	GlobalShortcutPause  string          `json:"global_shortcut_pause"`
	GlobalShortcutFreeze string          `json:"global_shortcut_freeze"`
	GlobalShortcutUndo   string          `json:"global_shortcut_undo"`
	ClickBudgetPerHour   int             `json:"click_budget_per_hour"`
	SuppressRapidMs      int             `json:"suppress_rapid_ms"`
	FrameConsistencyN    int             `json:"frame_consistency_n"`
	FrameConsistencyStd  float64         `json:"frame_consistency_std"`
	FrameConsistencyTTL  int             `json:"frame_consistency_ttl"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *AppConfig {
	return &AppConfig{
		Mode:                 ModeObserve,
		PerformanceTier:      PerfBalanced,
		InferThreads:         0, // 0 = 自动（物理核 50~70%）
		FpsLimit:             2.5,
		SaveScreenshots:      true,
		ScreenshotDays:       7,
		ScreenshotPath:       "",
		MouseFollow:          true,
		LogRetentionDays:     30,
		LogDir:               "",
		ModelPath:            "tangchuang/yolo26m.onnx",
		PolicyPath:           "configs/policy.yaml",
		PolicyPollSec:        600,
		NegFrameCapture:      false,
		NegFrameInterval:     30,
		OverlayEnabled:       false,
		GlobalShortcutPause:  "CmdOrCtrl+Shift+P",
		GlobalShortcutFreeze: "CmdOrCtrl+Shift+F",
		GlobalShortcutUndo:   "CmdOrCtrl+Shift+Z",
		ClickBudgetPerHour:   60,
		SuppressRapidMs:      2000,
		FrameConsistencyN:    1, // 1 = 检测到即点击（用户要求去掉多帧确认等待）
		FrameConsistencyStd:  4.0, // 1.0px 低于检测抖动会导致永不通过（引擎内另有 ≥2px 下限保护）
		FrameConsistencyTTL:  8000,
	}
}

// Heartbeat 全局状态条心跳数据
type Heartbeat struct {
	Mode           AppMode `json:"mode"`
	LastCaptureAgo string  `json:"last_capture_ago"`
	ActualFps      float64 `json:"actual_fps"`
	DroppedCount   int     `json:"dropped_count"`
	TodayBlocked   int     `json:"today_blocked"`
	TodayReported  int     `json:"today_reported"`
	TodayFalse     int     `json:"today_false"`
	Imgsz          int     `json:"imgsz"`
	ProfileDate    string  `json:"profile_date"`
}

// StatsDaily 日聚合统计
type StatsDaily struct {
	Date           time.Time `json:"date"`
	PopupsSeen     int       `json:"popups_seen"`
	AutoClosed     int       `json:"auto_closed"`
	FalsePositives int       `json:"false_positives"`
	AvgLatencyMs   int       `json:"avg_latency_ms"`
	TopApps        []AppStat `json:"top_apps"`
	CrashCount     int       `json:"crash_count"`
}

// AppStat 单应用统计
type AppStat struct {
	BundleID     string `json:"bundle_id"`
	DisplayName  string `json:"display_name"`
	TimesSeen    int    `json:"times_seen"`
	TimesClicked int    `json:"times_clicked"`
	TimesFailed  int    `json:"times_failed"`
}

// IsValidMode 检查是否为有效的运行模式
func IsValidMode(m AppMode) bool {
	switch m {
	case ModeObserve, ModeAuto, ModeExtreme, ModePaused, ModeDegraded,
		ModeUnauthorized, ModeSelftestPending, ModeSelftestFailed:
		return true
	}
	return false
}
