//go:build windows

package infer

// ============================================================
// Windows 侧 ONNX Runtime 检测器 —— 纯 Go 实现（无 cgo）
//
// 说明：原实现依赖 cgo 在链接期绑定 onnxruntime.lib，因而要求本机装有
// C 编译器。现改为通过 ort_runtime_windows.go 在运行时加载 onnxruntime.dll
// 并直接调用 OrtApi C API —— 同一份 DLL、同一套 API，推理结果一致，
// 但不再需要任何编译器，构建与运行都零前置依赖。
//
// 模型元信息（输入尺寸、类别数）仍从 ONNX session 实时读取，
// 与 cgo 版本行为一致；动态维（符号轴）返回 -1 的情况照样归零处理。
// ============================================================

import (
	"fmt"
	"image"
	"log"
	"runtime"
	"syscall"
	"unsafe"

	"screenguard/internal/types"
)

// ortModel 持有一个 ONNX session 及其元信息
type ortModel struct {
	session    uintptr // OrtSession*
	env        uintptr // OrtEnv*
	allocator  uintptr // OrtAllocator*
	inputName  uintptr // char*（由 ORT 默认分配器分配）
	outputName uintptr // char*
	inputW     int
	inputH     int
	numClasses int
}

// ONNXDetector 基于 ONNX Runtime 的检测器实现
type ONNXDetector struct {
	model       *ortModel
	modelPath   string
	threadCount int
}

// NewONNXDetector 创建 ONNX Runtime 检测器
func NewONNXDetector(modelPath string, threads int) (*ONNXDetector, error) {
	if err := ortInit(); err != nil {
		return nil, fmt.Errorf("初始化 ONNX Runtime 失败: %w", err)
	}

	if threads <= 0 {
		threads = 4
	}

	// Windows 的 CreateSession 需要 UTF-16 (wchar_t*)
	wModelPath, err := syscall.UTF16PtrFromString(modelPath)
	if err != nil {
		return nil, fmt.Errorf("模型路径编码失败 %s: %w", modelPath, err)
	}

	m := &ortModel{}

	logid := cstr("screenguard")
	if st := ortCall(pCreateEnv, uintptr(ortLoggingLevelWarning),
		uintptr(unsafe.Pointer(&logid[0])),
		uintptr(unsafe.Pointer(&m.env))); st != 0 {
		return nil, fmt.Errorf("创建 OrtEnv 失败: %s", ortError(st))
	}
	runtime.KeepAlive(logid)

	var opts uintptr
	if st := ortCall(pCreateSessionOptions, uintptr(unsafe.Pointer(&opts))); st != 0 || opts == 0 {
		return nil, fmt.Errorf("创建 SessionOptions 失败: %s", ortError(st))
	}

	ortCall(pSetIntraOpNumThreads, opts, uintptr(threads))
	ortCall(pSetInterOpNumThreads, opts, 1)
	ortCall(pSetSessionGraphOptimizationLevel, opts, uintptr(ortEnableAll))

	st := ortCall(pCreateSession, m.env,
		uintptr(unsafe.Pointer(wModelPath)), opts,
		uintptr(unsafe.Pointer(&m.session)))
	ortCall(pReleaseSessionOptions, opts)
	runtime.KeepAlive(wModelPath)
	if st != 0 {
		errMsg := ortError(st)
		ortCall(pReleaseEnv, m.env)
		return nil, fmt.Errorf("无法创建 ONNX session (%s): %s", modelPath, errMsg)
	}
	if m.session == 0 {
		return nil, fmt.Errorf("无法创建 ONNX session: %s", modelPath)
	}

	if st := ortCall(pGetAllocatorWithDefaultOptions, uintptr(unsafe.Pointer(&m.allocator))); st != 0 {
		m.allocator = 0
	}

	ortCall(pSessionGetInputName, m.session, 0, m.allocator, uintptr(unsafe.Pointer(&m.inputName)))
	ortCall(pSessionGetOutputName, m.session, 0, m.allocator, uintptr(unsafe.Pointer(&m.outputName)))

	m.readInputShape()
	m.readOutputShape()

	if ver, path, e := OrtRuntimeInfo(); e == nil {
		log.Printf("[infer] ONNX Runtime %s | 库=%s", ver, path)
	}
	log.Printf("[infer] ONNX 模型已加载: %s (输入=%dx%d, 类别=%d, 线程=%d)",
		modelPath, m.inputW, m.inputH, m.numClasses, threads)

	return &ONNXDetector{
		model:       m,
		modelPath:   modelPath,
		threadCount: threads,
	}, nil
}

// readInputShape 从 session 元数据读取输入尺寸
func (m *ortModel) readInputShape() {
	var typeInfo uintptr
	if ortCall(pSessionGetInputTypeInfo, m.session, 0, uintptr(unsafe.Pointer(&typeInfo))) != 0 {
		return
	}
	var shapeInfo uintptr
	if ortCall(pCastTypeInfoToTensorInfo, typeInfo, uintptr(unsafe.Pointer(&shapeInfo))) == 0 && shapeInfo != 0 {
		var dimCount uint64
		if ortCall(pGetDimensionsCount, shapeInfo, uintptr(unsafe.Pointer(&dimCount))) == 0 && dimCount == 4 {
			var dims [4]int64
			ortCall(pGetDimensions, shapeInfo, uintptr(unsafe.Pointer(&dims[0])), uintptr(dimCount))
			m.inputH = int(dims[2])
			m.inputW = int(dims[3])
		}
	}
	ortCall(pReleaseTypeInfo, typeInfo)

	// 动态维（符号轴）ORT 返回 -1 而不是 0，必须一起归零，
	// 否则 Go 侧 "inputW == 0" 的回退判断不成立，会拿 -1 去建张量。
	if m.inputW < 0 {
		m.inputW = 0
	}
	if m.inputH < 0 {
		m.inputH = 0
	}
}

// readOutputShape 从输出形状推断类别数
func (m *ortModel) readOutputShape() {
	var outTypeInfo uintptr
	if ortCall(pSessionGetOutputTypeInfo, m.session, 0, uintptr(unsafe.Pointer(&outTypeInfo))) != 0 {
		return
	}
	var outShape uintptr
	if ortCall(pCastTypeInfoToTensorInfo, outTypeInfo, uintptr(unsafe.Pointer(&outShape))) == 0 && outShape != 0 {
		var outDimCount uint64
		if ortCall(pGetDimensionsCount, outShape, uintptr(unsafe.Pointer(&outDimCount))) == 0 && outDimCount >= 3 {
			outDims := make([]int64, outDimCount)
			ortCall(pGetDimensions, outShape, uintptr(unsafe.Pointer(&outDims[0])), uintptr(outDimCount))
			if last := outDims[outDimCount-1]; last > 6 {
				m.numClasses = int(last) - 5
			}
		}
	}
	ortCall(pReleaseTypeInfo, outTypeInfo)

	if m.numClasses == 0 {
		m.numClasses = 10
	}
}

// Close 释放 session / env / 名称字符串
func (d *ONNXDetector) Close() {
	if d.model == nil {
		return
	}
	m := d.model
	d.model = nil

	// 名称由 ORT 默认分配器分配，需先于 session 释放
	if m.inputName != 0 && m.allocator != 0 {
		ortCall(pAllocatorFree, m.allocator, m.inputName)
		m.inputName = 0
	}
	if m.outputName != 0 && m.allocator != 0 {
		ortCall(pAllocatorFree, m.allocator, m.outputName)
		m.outputName = 0
	}
	if m.session != 0 {
		ortCall(pReleaseSession, m.session)
		m.session = 0
	}
	if m.env != 0 {
		ortCall(pReleaseEnv, m.env)
		m.env = 0
	}
}

// DetectLevel1 第一级：整屏 → imgsz → 只读容器类
func (d *ONNXDetector) DetectLevel1(img image.Image, imgsz int) ([]types.DetectionBox, int, error) {
	return d.detect(img, imgsz, image.Pt(0, 0))
}

// DetectLevel2 第二级：容器框外扩 15% 裁切 → imgsz2
func (d *ONNXDetector) DetectLevel2(roi image.Image, roiOrigin image.Point, imgsz2 int, screenW, screenH int) ([]types.DetectionBox, int, error) {
	return d.detect(roi, imgsz2, roiOrigin)
}

// Imgsz2 计算第二级 imgsz
func (d *ONNXDetector) Imgsz2(cropW, cropH int) int {
	return ComputeImgsz2(cropW, cropH)
}

// ModelInputSize 静态输入返回尺寸，动态输入返回 0（红线 C9）
func (d *ONNXDetector) ModelInputSize() int {
	if d.model == nil {
		return 0
	}
	w := d.model.inputW
	h := d.model.inputH
	if w > 0 && w == h {
		return w
	}
	return 0
}

// detect 核心推理 — 图像预处理 → ONNX 推理 → NMS → 坐标逆变换
func (d *ONNXDetector) detect(img image.Image, imgsz int, origin image.Point) ([]types.DetectionBox, int, error) {
	if d.model == nil {
		return nil, 0, fmt.Errorf("模型未加载")
	}
	m := d.model

	inputW := m.inputW
	inputH := m.inputH
	// 静态模型用模型自带尺寸；动态模型（<=0）用调用方给的 imgsz
	if inputW <= 0 || inputH <= 0 {
		inputW = imgsz
		inputH = imgsz
	}

	lb := ComputeLetterbox(img.Bounds().Dx(), img.Bounds().Dy(), imgsz)

	// 预处理: resize + letterbox + RGB → BGR + NCHW float32
	inputData := preprocessImage(img, inputW, inputH)

	// ---- 构建输入张量 ----
	var memInfo uintptr
	if st := ortCall(pCreateCpuMemoryInfo, uintptr(ortArenaAllocator), uintptr(ortMemTypeDefault),
		uintptr(unsafe.Pointer(&memInfo))); st != 0 {
		return nil, 0, fmt.Errorf("CreateCpuMemoryInfo 失败: %s", ortError(st))
	}

	shape := [4]int64{1, 3, int64(inputH), int64(inputW)}
	var inputTensor uintptr
	st := ortCall(pCreateTensorWithDataAsOrtValue, memInfo,
		uintptr(unsafe.Pointer(&inputData[0])),
		uintptr(len(inputData))*4,
		uintptr(unsafe.Pointer(&shape[0])), 4, uintptr(ortTypeFloat),
		uintptr(unsafe.Pointer(&inputTensor)))
	ortCall(pReleaseMemoryInfo, memInfo)
	runtime.KeepAlive(inputData)
	runtime.KeepAlive(shape)

	if st != 0 {
		return nil, 0, fmt.Errorf("创建输入张量失败: %s", ortError(st))
	}
	if inputTensor == 0 {
		return nil, 0, fmt.Errorf("创建输入张量失败")
	}

	// ---- 推理 ----
	inNames := [1]uintptr{m.inputName}
	outNames := [1]uintptr{m.outputName}
	inputs := [1]uintptr{inputTensor}

	var outputTensor uintptr
	st = ortCall(pRun, m.session, 0,
		uintptr(unsafe.Pointer(&inNames[0])),
		uintptr(unsafe.Pointer(&inputs[0])), 1,
		uintptr(unsafe.Pointer(&outNames[0])), 1,
		uintptr(unsafe.Pointer(&outputTensor)))
	ortCall(pReleaseValue, inputTensor)
	runtime.KeepAlive(inNames)
	runtime.KeepAlive(outNames)
	runtime.KeepAlive(inputs)

	if st != 0 {
		return nil, 0, fmt.Errorf("ONNX 推理失败 (input=%dx%d, imgsz=%d): %s",
			inputW, inputH, imgsz, ortError(st))
	}
	if outputTensor == 0 {
		return nil, 0, fmt.Errorf("ONNX 推理失败: Run 返回空输出张量")
	}

	// ---- 取回输出数据 ----
	var outData uintptr
	if st := ortCall(pGetTensorMutableData, outputTensor, uintptr(unsafe.Pointer(&outData))); st != 0 {
		ortCall(pReleaseValue, outputTensor)
		return nil, 0, fmt.Errorf("GetTensorMutableData 失败: %s", ortError(st))
	}

	var outTypeInfo, outShapeInfo uintptr
	var elemCount uint64
	if ortCall(pGetTypeInfo, outputTensor, uintptr(unsafe.Pointer(&outTypeInfo))) == 0 {
		if ortCall(pCastTypeInfoToTensorInfo, outTypeInfo, uintptr(unsafe.Pointer(&outShapeInfo))) == 0 && outShapeInfo != 0 {
			ortCall(pGetTensorShapeElementCount, outShapeInfo, uintptr(unsafe.Pointer(&elemCount)))
		}
		ortCall(pReleaseTypeInfo, outTypeInfo)
	}

	if elemCount == 0 {
		ortCall(pReleaseValue, outputTensor)
		return nil, 0, fmt.Errorf("输出张量元素数为 0")
	}

	// outData 指向 ORT 输出张量的缓冲区（非 Go 堆内存），
	// 在下方 ReleaseValue 之前一直有效，因此转为切片后立刻拷贝是安全的。
	out := make([]float32, elemCount)
	copy(out, unsafe.Slice((*float32)(unsafe.Pointer(outData)), int(elemCount)))
	ortCall(pReleaseValue, outputTensor)

	// ---- 后处理 ----
	boxes := postprocess(out, m.numClasses, inputW, inputH, lb, origin)
	return boxes, 0, nil
}
