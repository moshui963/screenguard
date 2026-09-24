package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"screenguard/internal/engine"
	"screenguard/internal/types"
)

// ============================================================
// 策略引擎 — PRD 3.4 节
// 实现 policy.yaml 解析、schema 校验、热加载、模拟重放
// ============================================================

// PolicyYAML policy.yaml 的结构
type PolicyYAML struct {
	Version   string                 `yaml:"version"`
	Classes   map[string]ClassPolicy `yaml:"classes"`
	Forbidden map[string]bool        `yaml:"forbidden_zones"`
	Blacklist []BlacklistYAML        `yaml:"blacklist"`
}

// ClassPolicy 逐类策略
type ClassPolicy struct {
	Conf float64          `yaml:"conf"`
	Mode types.PolicyMode `yaml:"mode"`
}

// BlacklistYAML YAML 格式的黑名单
type BlacklistYAML struct {
	Type   string `yaml:"type"`
	Value  string `yaml:"value"`
	Reason string `yaml:"reason"`
	TTL    string `yaml:"ttl"`
	Source string `yaml:"source"`
}

// Manager 策略管理器
type Manager struct {
	mu         sync.RWMutex
	current    *engine.PolicySnapshot
	policyPath string
}

// NewManager 创建策略管理器
func NewManager(policyPath string) *Manager {
	return &Manager{policyPath: policyPath}
}

// LoadAndApply 加载 policy.yaml 并生成快照
func (m *Manager) LoadAndApply() (*engine.PolicySnapshot, error) {
	data, err := os.ReadFile(m.policyPath)
	if err != nil {
		return nil, fmt.Errorf("读取策略文件: %w", err)
	}

	var py PolicyYAML
	if err := yaml.Unmarshal(data, &py); err != nil {
		return nil, fmt.Errorf("解析策略 YAML: %w", err)
	}

	// schema 校验
	if err := validate(&py); err != nil {
		return nil, fmt.Errorf("策略校验失败: %w", err)
	}

	snap := &engine.PolicySnapshot{
		Version:        py.Version,
		ConfThresholds: make(map[string]float64),
		ClassModes:     make(map[string]types.PolicyMode),
		ForbiddenZones: py.Forbidden,
	}

	for cls, cp := range py.Classes {
		snap.ConfThresholds[cls] = cp.Conf
		snap.ClassModes[cls] = cp.Mode
	}

	// 黑名单
	for _, bl := range py.Blacklist {
		var expires time.Time
		if bl.TTL != "" {
			d, err := time.ParseDuration(bl.TTL)
			if err == nil {
				expires = time.Now().Add(d)
			}
		}
		snap.Blacklist = append(snap.Blacklist, engine.BlacklistEntry{
			Type:    bl.Type,
			Value:   bl.Value,
			Expires: expires,
		})
	}

	m.mu.Lock()
	m.current = snap
	m.mu.Unlock()

	return snap, nil
}

// Current 获取当前策略快照（线程安全）
func (m *Manager) Current() *engine.PolicySnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// GetYAML 读取策略文件原始 YAML 内容
func (m *Manager) GetYAML() (string, error) {
	data, err := os.ReadFile(m.policyPath)
	if err != nil {
		// 文件不存在时返回默认策略
		py := DefaultPolicy()
		data, _ := yaml.Marshal(py)
		return string(data), nil
	}
	return string(data), nil
}

// SaveYAML 保存策略 YAML 并热加载
func (m *Manager) SaveYAML(yamlStr, note string) error {
	// 先校验
	var py PolicyYAML
	if err := yaml.Unmarshal([]byte(yamlStr), &py); err != nil {
		return fmt.Errorf("YAML 语法错误: %w", err)
	}
	if err := validate(&py); err != nil {
		return fmt.Errorf("策略校验失败: %w", err)
	}
	// 写入文件
	if err := os.WriteFile(m.policyPath, []byte(yamlStr), 0644); err != nil {
		return fmt.Errorf("写入策略文件: %w", err)
	}
	// 热加载
	if _, err := m.LoadAndApply(); err != nil {
		return fmt.Errorf("热加载失败: %w", err)
	}
	log.Printf("[policy] 策略已保存并热加载, note=%s", note)
	return nil
}

// HashYAML 计算 YAML 内容的 sha256
func HashYAML(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// validate 策略 schema 校验 — PRD 异常处理：语法/schema 错误时已生效版本继续运行
func validate(py *PolicyYAML) error {
	if py.Version == "" {
		return fmt.Errorf("策略缺少 version 字段")
	}
	for cls, cp := range py.Classes {
		if cp.Conf < 0.05 || cp.Conf > 0.95 {
			return fmt.Errorf("类别 %s 的阈值 %.2f 超出合法区间 [0.05, 0.95]", cls, cp.Conf)
		}
		switch cp.Mode {
		case types.PolicyClick, types.PolicyReport, types.PolicyDisabled:
		default:
			return fmt.Errorf("类别 %s 的模式 %s 无效", cls, cp.Mode)
		}
	}
	// 默认禁区全开
	if py.Forbidden == nil {
		py.Forbidden = map[string]bool{
			"fullscreen_app":      true,
			"screen_lock":         true,
			"screen_share":        true,
			"remote_desktop":      true,
			"low_battery":         true,
			"install_wizard":      true,
			"uac_security_prompt": true,
		}
	}
	return nil
}

// DefaultPolicy 返回默认策略（与 popup_infer.py 的 CONF/POLICY 对齐）
func DefaultPolicy() *PolicyYAML {
	return &PolicyYAML{
		Version: "v1-default",
		Classes: map[string]ClassPolicy{
			"TanChuang":  {Conf: 0.40, Mode: types.PolicyDisabled},
			"GuanBi":     {Conf: 0.35, Mode: types.PolicyClick},
			"GuanBi_str": {Conf: 0.45, Mode: types.PolicyClick},
			"ZhiDaoLe":   {Conf: 0.45, Mode: types.PolicyClick},
			"TiaoGuo":    {Conf: 0.45, Mode: types.PolicyClick},
			"WoBuShou":   {Conf: 0.45, Mode: types.PolicyClick},
			"QuXiao":     {Conf: 0.55, Mode: types.PolicyClick},
			"YunXu":      {Conf: 0.65, Mode: types.PolicyReport},
			"TuiChu":     {Conf: 0.70, Mode: types.PolicyReport},
			"XiaYiBu":    {Conf: 0.75, Mode: types.PolicyReport},
		},
		Forbidden: map[string]bool{
			"fullscreen_app":      true,
			"screen_lock":         true,
			"screen_share":        true,
			"remote_desktop":      true,
			"low_battery":         true,
			"install_wizard":      true,
			"uac_security_prompt": true,
		},
	}
}

// WriteDefault 写入默认策略文件
func WriteDefault(path string) error {
	py := DefaultPolicy()
	data, err := yaml.Marshal(py)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// CategoryPolicy 前端逐类策略编辑的序列化结构
type CategoryPolicy struct {
	Conf float64 `json:"conf"`
	Mode string  `json:"mode"`
}

// GetCategoryPolicy 返回当前快照的逐类策略（conf + mode）
// 用于前端调试台"类别控制"的初始化加载，保证 UI 与后端快照一致。
func (m *Manager) GetCategoryPolicy() map[string]CategoryPolicy {
	m.mu.RLock()
	snap := m.current
	m.mu.RUnlock()

	out := map[string]CategoryPolicy{}
	if snap == nil {
		return out
	}
	for cls, mode := range snap.ClassModes {
		cp := CategoryPolicy{Mode: string(mode)}
		if c, ok := snap.ConfThresholds[cls]; ok {
			cp.Conf = c
		}
		out[cls] = cp
	}
	return out
}

// SaveCategoryPolicy 合并更新逐类策略并热加载。
// updates 为 {类名: {conf, mode}}，只覆盖传入的类别，其余保持原值。
// 写回 policy.yaml 后重新 LoadAndApply，返回新快照供调用方下发给引擎。
func (m *Manager) SaveCategoryPolicy(updates map[string]CategoryPolicy) (*engine.PolicySnapshot, error) {
	data, err := os.ReadFile(m.policyPath)
	var py PolicyYAML
	if err != nil {
		// 文件不存在 → 以默认策略为基础叠加
		py = *DefaultPolicy()
	} else {
		if err := yaml.Unmarshal(data, &py); err != nil {
			return nil, fmt.Errorf("解析策略 YAML: %w", err)
		}
	}
	if py.Classes == nil {
		py.Classes = map[string]ClassPolicy{}
	}

	for cls, up := range updates {
		switch types.PolicyMode(up.Mode) {
		case types.PolicyClick, types.PolicyReport, types.PolicyDisabled:
		default:
			return nil, fmt.Errorf("类别 %s 的模式 %q 无效", cls, up.Mode)
		}
		if up.Conf < 0.05 || up.Conf > 0.95 {
			return nil, fmt.Errorf("类别 %s 的阈值 %.2f 超出合法区间 [0.05, 0.95]", cls, up.Conf)
		}
		py.Classes[cls] = ClassPolicy{Conf: up.Conf, Mode: types.PolicyMode(up.Mode)}
	}

	if err := validate(&py); err != nil {
		return nil, fmt.Errorf("策略校验失败: %w", err)
	}
	out, err := yaml.Marshal(&py)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(m.policyPath, out, 0644); err != nil {
		return nil, fmt.Errorf("写入策略文件: %w", err)
	}
	snap, err := m.LoadAndApply()
	if err != nil {
		return nil, fmt.Errorf("热加载失败: %w", err)
	}
	log.Printf("[policy] 逐类策略已保存并热加载, version=%s", snap.Version)
	return snap, nil
}
