// Command ortcheck 验证 ONNX Runtime 推理链路是否真正可用。
//
// 它完成三件事：
//  1. 加载 onnxruntime.dll 并读取运行时版本（验证动态加载链路）
//  2. 用真实模型 yolo26m.onnx 建立 session（验证 OrtApi 虚函数表偏移正确）
//  3. 用项目自带自检模块对合成弹窗探针图跑两级检测（验证端到端推理结果正确）
//
// 用法: ortcheck.exe [模型路径]
package main

import (
	"fmt"
	"os"

	"screenguard/internal/infer"
	"screenguard/internal/selftest"
)

func main() {
	modelPath := "models/yolo26m.onnx"
	if len(os.Args) > 1 {
		modelPath = os.Args[1]
	}

	ver, dll, err := infer.OrtRuntimeInfo()
	if err != nil {
		fmt.Printf("[FAIL] ONNX Runtime 初始化失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[OK] ONNX Runtime 版本: %s\n", ver)
	fmt.Printf("[OK] 动态库路径: %s\n", dll)

	d, err := infer.NewONNXDetector(modelPath, 4)
	if err != nil {
		fmt.Printf("[FAIL] 创建检测器失败: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	fmt.Printf("[OK] 模型已加载: %s (静态输入尺寸=%d, 0 表示动态)\n", modelPath, d.ModelInputSize())

	probe := selftest.DefaultProbe(1920, 1080)
	img := selftest.DrawProbe(probe)

	res, err := selftest.Run(img, probe, d, []int{1024, 1280, 1536}, modelPath)
	if err != nil {
		fmt.Printf("[FAIL] 自检执行失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("自检结论: allPassed=%v recommended=%d reason=%s\n",
		res.AllPassed, res.Recommended, res.Reason)
	for _, r := range res.Results {
		fmt.Printf("  imgsz=%-5d 容器检出=%-5v 出口检出=%-5v 容器偏差=%6.1fpx 出口偏差=%6.1fpx 耗时=%4dms pass=%v\n",
			r.Imgsz, r.ContainerFound, r.ExitFound, r.ContainerBias, r.ExitBias, r.ModelMs, r.Pass)
	}
	if res.Profile != nil {
		fmt.Printf("推荐档位: imgsz=%d\n", res.Profile.Imgsz)
	}

	if !res.AllPassed {
		fmt.Println("[WARN] 自检未全部通过（见上表）")
		os.Exit(2)
	}
	fmt.Println("[OK] 推理链路端到端验证通过")
}
