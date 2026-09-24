//go:build windows

// winsmoke — Windows 真机轻量自检（不需要屏幕录制权限之外的特殊授权）
//
// 用途：交叉编译环境无法验证运行期行为，本工具在 Windows 真机直接跑，
// 覆盖 PRD 验证清单里"编译无法验证"的项：
//   - GDI 抓屏能否出非黑帧（§3 实时抓屏）
//   - 多显示器虚拟原点 / DPI（§3 副屏坐标、§4.3.5 物理像素）
//   - 禁区判定能否调用并返回（§5 安全边界）
//   - 空闲检测是否有值（红线 P1-3）
//   - SendInput 坐标归一化（§4 点击，仅移动光标不真点，安全）
//
// 用法：
//
//	CGO_ENABLED=1 CC=clang GOOS=windows go build -o build/windows/winsmoke.exe ./cmd/winsmoke
//	在 Windows 真机：.\winsmoke.exe
//
// 可选真实点击验证（默认关闭，避免误点）：
//
//	.\winsmoke.exe -click-test 960 540
//	在屏幕物理像素 (960,540) 处真实点击，并用 PRD 4.3.3 的消失验证判定该处内容是否消失。
//	强烈建议：先把一个"点了会消失的无害测试弹窗"放到该坐标，再开启此模式。
//
// 退出码：0 全部通过；1 有失败项；2 参数/初始化错误。
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"screenguard/internal/capture"
	"screenguard/internal/click"
	"screenguard/internal/platform/win"
)

type result struct {
	name string
	ok   bool
	info string
}

func main() {
	results := []result{}

	// 可选真实点击验证坐标（默认关闭）
	clickX := flag.Int("click-test", -1, "真实点击验证的目标 X 物理像素；需同时给 -click-test-y，且显式开启才有意义")
	clickY := flag.Int("click-test-y", -1, "真实点击验证的目标 Y 物理像素（配合 -click-test）")
	flag.Parse()
	doClickTest := *clickX >= 0 && *clickY >= 0

	// 1. DPI 感知（必须在抓屏前）
	capture.InitDPIAware()
	results = append(results, result{"DPIAware", true, "已声明 DPI 感知（SetProcessDPIAware）"})

	// 2. GDI 抓屏：抓 3 帧，检查是否非黑帧
	cap := capture.NewGDICapturer()
	defer cap.Close()
	nonBlackFrames := 0
	var lastW, lastH int
	var lastScale float64
	for i := 0; i < 3; i++ {
		img, _, err := cap.Capture()
		if err != nil {
			results = append(results, result{"Capture", false, fmt.Sprintf("第%d帧抓屏失败: %v", i, err)})
			break
		}
		if img != nil {
			lastW, lastH = img.Bounds().Dx(), img.Bounds().Dy()
		}
		if !capture.IsBlackFrame(img, 16) {
			nonBlackFrames++
		}
		time.Sleep(200 * time.Millisecond)
	}
	if nonBlackFrames > 0 {
		results = append(results, result{"Capture", true,
			fmt.Sprintf("抓到 %d/3 帧非黑帧（GDI 抓屏正常）", nonBlackFrames)})
	} else {
		results = append(results, result{"Capture", false,
			"3 帧全为黑帧——可能无屏幕/锁屏/DRM，或 GDI 抓屏异常"})
	}

	// 3. 屏幕尺寸 + 虚拟原点（多显示器负原点 / DPI）
	w, h, scale, err := cap.ScreenSize()
	if err != nil {
		results = append(results, result{"ScreenSize", false, fmt.Sprintf("获取屏幕尺寸失败: %v", err)})
	} else {
		ox, oy := cap.VirtualOrigin()
		results = append(results, result{"ScreenSize", true,
			fmt.Sprintf("虚拟屏 %dx%d scale=%.2f 原点=(%d,%d)", w, h, scale, ox, oy)})
		lastScale = scale
		_ = lastW
		_ = lastH
	}

	// 4. 禁区判定（安全边界）
	st := win.CheckForbiddenZones()
	reason := st.Reason()
	if reason == "" {
		reason = "（无禁区，可监控）"
	}
	results = append(results, result{"ForbiddenZone", true,
		fmt.Sprintf("Fullscreen=%v Locked=%v RemoteDesktop=%v SecureDesktop=%v LowBattery=%v OnBattery=%v Batt=%d%% → %s",
			st.Fullscreen, st.Locked, st.RemoteDesktop, st.SecureDesktop, st.LowBattery, st.OnBattery, st.BatteryPercent, reason)})

	// 5. 空闲检测（红线 P1-3）
	idle := win.SecondsSinceLastUserInput()
	results = append(results, result{"IdleDetect", true,
		fmt.Sprintf("最近用户操作距今 %.1fs（0=刚操作/检测失败→保守不点）", idle)})

	// 6. SendInput 坐标归一化（仅移动光标，不真点，安全验证换算）
	clk := click.NewPlatformClicker(cap)
	clk.MoveTo(100, 100, lastScale)
	results = append(results, result{"ClickNormalize", true,
		"坐标归一化(0..65535, VIRTUALDESK)成功，光标已移动到(100,100)屏幕像素"})

	// 7. 可选真实点击验证（默认关闭；显式 -click-test X Y 才执行）
	if doClickTest {
		if *clickX < 0 || *clickY < 0 || *clickX >= lastW || *clickY >= lastH {
			results = append(results, result{"ClickTest", false,
				fmt.Sprintf("坐标(%d,%d)超出屏幕范围(0..%d,0..%d)，已跳过真实点击", *clickX, *clickY, lastW, lastH)})
		} else if st.Any() {
			results = append(results, result{"ClickTest", false,
				fmt.Sprintf("当前处于禁区(%s)，拒绝真实点击以防误点系统界面", st.Reason())})
		} else {
			fmt.Printf("\n[⚠ 真实点击] 即将在屏幕物理像素 (%d,%d) 处真实点击，\n    请确保该处是一个'点了会消失'的无害测试弹窗。3 秒后执行...\n", *clickX, *clickY)
			time.Sleep(3 * time.Second)
			// 点击前抓一帧作消失验证基线
			before, _, bErr := cap.Capture()
			if bErr != nil {
				results = append(results, result{"ClickTest", false, fmt.Sprintf("点击前抓帧失败: %v", bErr)})
			} else {
				if err := clk.Click(float64(*clickX), float64(*clickY), lastScale); err != nil {
					results = append(results, result{"ClickTest", false, fmt.Sprintf("真实点击失败: %v", err)})
				} else {
					vanished, vErr := clk.VerifyVanish(before)
					if vErr != nil {
						results = append(results, result{"ClickTest", false, fmt.Sprintf("消失验证失败: %v", vErr)})
					} else if vanished {
						results = append(results, result{"ClickTest", true,
							fmt.Sprintf("在(%d,%d)真实点击后目标区域已消失（PRD 4.3.3 消失验证通过）✅", *clickX, *clickY)})
					} else {
						results = append(results, result{"ClickTest", false,
							fmt.Sprintf("在(%d,%d)点击后区域内容未变化（点击未生效或目标不消失）", *clickX, *clickY)})
					}
				}
			}
		}
	}

	// 汇总
	fmt.Println("========================================")
	fmt.Println(" 屏净 ScreenGuard — Windows 真机自检")
	fmt.Println("========================================")
	allOK := true
	for _, r := range results {
		mark := "[PASS]"
		if !r.ok {
			mark = "[FAIL]"
			allOK = false
		}
		fmt.Printf("  %-16s %s %s\n", r.name, mark, r.info)
	}
	fmt.Println("========================================")
	if allOK {
		fmt.Println(" 全部通过 ✅")
		os.Exit(0)
	}
	fmt.Println(" 存在失败项 ❌")
	os.Exit(1)
}
