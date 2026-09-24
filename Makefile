APP_NAME := screenguard
BUILD_DIR := build
FRONTEND_DIR := frontend
GO_LDFLAGS := -s -w

.PHONY: all frontend deps dev clean bump-version win win-backend win-package win-verify win-smoke

# ---- 依赖安装 ----
deps:
	cd $(FRONTEND_DIR) && npm install
	go mod tidy

# ---- 前端构建 ----
frontend:
	cd $(FRONTEND_DIR) && npm run build

# ---- 版本号：每次编译本体 +0.01（百分位自增，满 100 进位到主版本）----
# 源真值见 version.txt；构建脚本（本文件 win 目标 / scripts/build-win.ps1）会自增并回写。
bump-version:
	@v=$$(cat version.txt 2>/dev/null || echo 0.10); \
	m=$${v#*.}; M=$${v%.*}; \
	m=$$((10#$$m + 1)); \
	if [ $$m -ge 100 ]; then M=$$((M+1)); m=0; fi; \
	printf '%s.%02d' $$M $$m > version.txt; \
	echo "[version] -> $$(cat version.txt)"

# 模型与默认策略（随 app 分发的只读资源）
MODEL ?= yolo模型/yolo26m.onnx

# ============================================================
# Windows 构建（PRD: 桌面端软件标准，按 win 平台交付）
#
# 本机构建不再需要任何 C 编译器：
#   · ONNX Runtime 由 internal/infer 在运行时动态加载 onnxruntime.dll
#     （LoadLibrary + OrtApi 虚函数表），不再于链接期绑定 onnxruntime.lib；
#   · SQLite 使用纯 Go 驱动（glebarez/go-sqlite，基于 modernc.org/sqlite）。
# 因此整个后端是纯 Go 的，CGO_ENABLED=0 亦可完整构建，且不损失任何功能。
# ============================================================
WIN_ORT_LIB := third_party/onnxruntime-win-x64-1.19.2/lib
WIN_OUT := $(BUILD_DIR)/windows

# ---- Windows 后端构建（纯 Go；编译本体时自动 +0.01）----
win-backend:
	@test -f "$(WIN_ORT_LIB)/onnxruntime.dll" || (echo "[ERR] 缺少 $(WIN_ORT_LIB)/onnxruntime.dll" && exit 1)
	@$(MAKE) bump-version
	VER=$$(cat version.txt); \
	mkdir -p $(WIN_OUT); \
	CGO_ENABLED=0 \
		go build -ldflags "$(GO_LDFLAGS) -X screenguard/internal/version.Version=$$VER" \
		-o $(WIN_OUT)/$(APP_NAME).exe ./cmd/$(APP_NAME)

# ---- 组装 Windows 分发目录 ----
win-package: win-backend
	mkdir -p $(WIN_OUT)/models $(WIN_OUT)/configs
	cp $(WIN_ORT_LIB)/onnxruntime.dll $(WIN_OUT)/
	cp $(MODEL) $(WIN_OUT)/models/yolo26m.onnx
	cp configs/policy.yaml $(WIN_OUT)/configs/policy.yaml
	cp configs/config.json $(WIN_OUT)/configs/config.json
	cp build/tray_*.png $(WIN_OUT)/
	@echo "Windows 分发目录已生成 -> $(WIN_OUT)/"
	@echo "  含: screenguard.exe, onnxruntime.dll, models/yolo26m.onnx, configs/, 托盘图标"

# ---- Windows 全量构建（前端 + 后端 + 打包）----
win: frontend win-package

# ---- 推理链路端到端验证（真实模型 + 合成弹窗探针，无需真机抓屏）----
win-verify: win-package
	CGO_ENABLED=0 go build -ldflags "$(GO_LDFLAGS)" -o $(WIN_OUT)/ortcheck.exe ./cmd/ortcheck
	cd $(WIN_OUT) && ./ortcheck.exe

# ---- Windows 真机自检工具（不依赖 GUI；不触发版本号自增）----
win-smoke:
	@test -f "$(WIN_ORT_LIB)/onnxruntime.dll" || (echo "[ERR] 缺少 $(WIN_ORT_LIB)/onnxruntime.dll" && exit 1)
	mkdir -p $(WIN_OUT)
	CGO_ENABLED=0 \
		go build -ldflags "$(GO_LDFLAGS)" \
		-o $(WIN_OUT)/winsmoke.exe ./cmd/winsmoke

# ---- 开发模式（热重载）----
dev:
	cd $(FRONTEND_DIR) && npm run dev & \
	CGO_ENABLED=0 go run ./cmd/$(APP_NAME) --dev

# ---- 清理 ----
clean:
	rm -rf $(BUILD_DIR)/windows $(FRONTEND_DIR)/dist
	rm -f screenguard frameprobe winsmoke

# ---- 默认目标 ----
all: win
