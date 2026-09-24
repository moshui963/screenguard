//go:build windows

package infer

// ============================================================
// ONNX Runtime 运行时加载层 —— 纯 Go，不依赖 cgo
//
// 为什么不再用 cgo：
//   原实现通过 `#cgo LDFLAGS: -lonnxruntime` 在链接期绑定 onnxruntime.lib，
//   这要求本机必须装有 C 编译器（gcc/clang），否则整个后端无法构建。
//   而 onnxruntime.dll 本身就是编译好的原生库：它在 DLL 里导出 OrtGetApiBase，
//   拿到 OrtApi 虚函数表后即可直接用 syscall 调用全部 C API。
//   这样既不需要编译器，推理能力也与 cgo 版本完全一致（同一份 DLL、同一套 API）。
//
// 虚函数表下标的来源：
//   ort_api_table.gen.go，由 scripts/gen_ort_table.py 从官方头文件
//   onnxruntime_c_api.h 解析生成。手写偏移量极易错位且错位即崩溃，故必须程序生成。
// ============================================================

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// ortAPIVersion 必须与 onnxruntime_c_api.h 中的 ORT_API_VERSION 一致。
const ortAPIVersion = 19

// 与 onnxruntime_c_api.h 保持一致的常量
const (
	ortLoggingLevelWarning = 2 // OrtLoggingLevel

	ortEnableAll = 99 // GraphOptimizationLevel: ORT_ENABLE_ALL

	ortArenaAllocator = 0 // OrtAllocatorType
	ortMemTypeDefault = 0 // OrtMemType

	ortTypeFloat = 1 // ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT
)

var (
	ortOnce      sync.Once
	ortInitError error
	ortDLLHandle syscall.Handle
	ortDLLPath   string
	ortAPIBase   uintptr
	ortAPI       uintptr // OrtApi* —— 直接指向函数指针数组
)

// 绑定的 ORT C API 函数指针（ortAPI 虚函数表中的项）
var (
	pGetVersionString uintptr // 来自 OrtApiBase
	pGetErrorMessage  uintptr
	pReleaseStatus    uintptr
	pCreateEnv        uintptr
	pReleaseEnv       uintptr

	pCreateSessionOptions             uintptr
	pReleaseSessionOptions            uintptr
	pSetIntraOpNumThreads             uintptr
	pSetInterOpNumThreads             uintptr
	pSetSessionGraphOptimizationLevel uintptr
	pCreateSession                    uintptr
	pReleaseSession                   uintptr
	pGetAllocatorWithDefaultOptions   uintptr
	pAllocatorFree                    uintptr
	pSessionGetInputName              uintptr
	pSessionGetOutputName             uintptr
	pSessionGetInputTypeInfo          uintptr
	pSessionGetOutputTypeInfo         uintptr
	pCastTypeInfoToTensorInfo         uintptr
	pGetDimensionsCount               uintptr
	pGetDimensions                    uintptr
	pGetTensorShapeElementCount       uintptr
	pReleaseTypeInfo                  uintptr
	pCreateCpuMemoryInfo              uintptr
	pReleaseMemoryInfo                uintptr
	pCreateTensorWithDataAsOrtValue   uintptr
	pReleaseValue                     uintptr
	pRun                              uintptr
	pGetTensorMutableData             uintptr
	pGetTypeInfo                      uintptr
)

// ortInit 幂等地完成 DLL 加载与函数绑定。
func ortInit() error {
	ortOnce.Do(func() { ortInitError = ortLoad() })
	return ortInitError
}

func ortLoad() error {
	idx := make(map[string]int, len(ortApiFuncNames))
	for i, n := range ortApiFuncNames {
		if _, dup := idx[n]; dup {
			return fmt.Errorf("ORT 虚函数表出现重名 %q，偏移表不可信", n)
		}
		idx[n] = i
	}

	path, err := locateOrtDLL()
	if err != nil {
		return err
	}
	ortDLLPath = path

	// 把 DLL 所在目录加入搜索路径，使 onnxruntime_providers_shared.dll 等
	// 同级依赖可被解析（Windows 默认不会搜索被加载 DLL 自己的目录）。
	if dir, e := syscall.UTF16PtrFromString(filepath.Dir(path)); e == nil {
		proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetDllDirectoryW")
		proc.Call(uintptr(unsafe.Pointer(dir)))
		runtime.KeepAlive(dir)
	}

	h, err := syscall.LoadLibrary(path)
	if err != nil {
		return fmt.Errorf("加载 %s 失败: %w", path, err)
	}
	ortDLLHandle = h

	getBase, err := syscall.GetProcAddress(h, "OrtGetApiBase")
	if err != nil {
		return fmt.Errorf("在 %s 中未找到 OrtGetApiBase: %w", path, err)
	}
	base, _, _ := syscall.SyscallN(uintptr(getBase))
	if base == 0 {
		return fmt.Errorf("%s 的 OrtGetApiBase() 返回空指针", path)
	}
	ortAPIBase = base

	// OrtApiBase 布局: [0] GetApi, [1] GetVersionString
	pGetVersionString = readPtr(base + ptrSize)

	api, _, _ := syscall.SyscallN(readPtr(base), uintptr(ortAPIVersion))
	if api == 0 {
		return fmt.Errorf("GetApi(ORT_API_VERSION=%d) 失败：%s 版本过旧", ortAPIVersion, path)
	}
	ortAPI = api

	return bindOrtProcs(idx)
}

func bindOrtProcs(idx map[string]int) error {
	var missing []string
	bind := func(name string) uintptr {
		i, ok := idx[name]
		if !ok {
			missing = append(missing, name)
			return 0
		}
		return readPtr(ortAPI + uintptr(i)*ptrSize)
	}

	pGetErrorMessage = bind("GetErrorMessage")
	pReleaseStatus = bind("ReleaseStatus")
	pCreateEnv = bind("CreateEnv")
	pReleaseEnv = bind("ReleaseEnv")
	pCreateSessionOptions = bind("CreateSessionOptions")
	pReleaseSessionOptions = bind("ReleaseSessionOptions")
	pSetIntraOpNumThreads = bind("SetIntraOpNumThreads")
	pSetInterOpNumThreads = bind("SetInterOpNumThreads")
	pSetSessionGraphOptimizationLevel = bind("SetSessionGraphOptimizationLevel")
	pCreateSession = bind("CreateSession")
	pReleaseSession = bind("ReleaseSession")
	pGetAllocatorWithDefaultOptions = bind("GetAllocatorWithDefaultOptions")
	pAllocatorFree = bind("AllocatorFree")
	pSessionGetInputName = bind("SessionGetInputName")
	pSessionGetOutputName = bind("SessionGetOutputName")
	pSessionGetInputTypeInfo = bind("SessionGetInputTypeInfo")
	pSessionGetOutputTypeInfo = bind("SessionGetOutputTypeInfo")
	pCastTypeInfoToTensorInfo = bind("CastTypeInfoToTensorInfo")
	pGetDimensionsCount = bind("GetDimensionsCount")
	pGetDimensions = bind("GetDimensions")
	pGetTensorShapeElementCount = bind("GetTensorShapeElementCount")
	pReleaseTypeInfo = bind("ReleaseTypeInfo")
	pCreateCpuMemoryInfo = bind("CreateCpuMemoryInfo")
	pReleaseMemoryInfo = bind("ReleaseMemoryInfo")
	pCreateTensorWithDataAsOrtValue = bind("CreateTensorWithDataAsOrtValue")
	pReleaseValue = bind("ReleaseValue")
	pRun = bind("Run")
	pGetTensorMutableData = bind("GetTensorMutableData")
	pGetTypeInfo = bind("GetTypeInfo")

	if len(missing) > 0 {
		return fmt.Errorf("ORT 虚函数表缺少必需函数: %s", strings.Join(missing, ", "))
	}
	return nil
}

// locateOrtDLL 按优先级定位 onnxruntime.dll：
//  1. 环境变量 SCREENGUARD_ORT_DLL（显式覆盖，便于排障）
//  2. exe 同目录（分发形态：DLL 与 exe 一起分发）
//  3. exe 向上若干级的 third_party/.../lib（开发形态）
//  4. 当前工作目录及其 third_party
//  5. 系统搜索路径
func locateOrtDLL() (string, error) {
	rel := filepath.Join("third_party", "onnxruntime-win-x64-1.19.2", "lib", "onnxruntime.dll")

	var cands []string
	add := func(p string) {
		if p != "" {
			cands = append(cands, p)
		}
	}

	if env := os.Getenv("SCREENGUARD_ORT_DLL"); env != "" {
		add(env)
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		add(filepath.Join(dir, "onnxruntime.dll"))
		add(filepath.Join(dir, "lib", "onnxruntime.dll"))
		for i := 0; i < 4; i++ {
			add(filepath.Join(dir, rel))
			dir = filepath.Dir(dir)
		}
	}

	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, "onnxruntime.dll"))
		add(filepath.Join(wd, rel))
	}

	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}

	// 最后交给系统搜索路径（PATH 等）
	if h, err := syscall.LoadLibrary("onnxruntime.dll"); err == nil {
		syscall.FreeLibrary(h)
		return "onnxruntime.dll", nil
	}

	return "", fmt.Errorf("未找到 onnxruntime.dll，已尝试以下位置：\n  %s", strings.Join(cands, "\n  "))
}

// OrtRuntimeInfo 返回实际加载的 onnxruntime 版本与路径，用于启动日志排障。
func OrtRuntimeInfo() (version string, path string, err error) {
	if err = ortInit(); err != nil {
		return "", "", err
	}
	if pGetVersionString != 0 {
		r, _, _ := syscall.SyscallN(pGetVersionString)
		version = goString(r)
	}
	return version, ortDLLPath, nil
}

// ---- 底层辅助 ----

const ptrSize = unsafe.Sizeof(uintptr(0))

// readPtr 读取 OrtApi 虚函数表中的函数指针。
//
// addr 指向 ONNX Runtime 自己分配的内存（非 Go 堆），不会被 GC 移动或回收，
// 因此这里的 uintptr → unsafe.Pointer 转换是安全的。
// （go vet 的 unsafeptr 无法判断指针来自外部库，会给出告警，属误报。）
func readPtr(addr uintptr) uintptr {
	return *(*uintptr)(unsafe.Pointer(addr))
}

// ortCall 调用一个 ORT C API。所有函数均为 __stdcall，
// x64 下与默认调用约定一致，故可直接用 SyscallN 传 uintptr 参数。
func ortCall(fn uintptr, args ...uintptr) uintptr {
	if fn == 0 || len(args) == 0 {
		return 0
	}
	r1, _, _ := syscall.SyscallN(fn, args...)
	return r1
}

// goString 把 ORT 返回的 const char* 转成 Go 字符串。
//
// 该指针由 ONNX Runtime 持有（非 Go 堆），在对应对象释放前持续有效，
// 因此逐字节读取并转换是安全的。
func goString(p uintptr) string {
	if p == 0 {
		return ""
	}
	buf := make([]byte, 0, 64)
	for i := 0; i < (1 << 20); i++ {
		c := *(*byte)(unsafe.Pointer(p + uintptr(i)))
		if c == 0 {
			break
		}
		buf = append(buf, c)
	}
	return string(buf)
}

// ortError 取出 OrtStatus 的错误信息并释放它。
func ortError(status uintptr) string {
	if status == 0 {
		return "未知错误（OrtStatus 为空）"
	}
	msg := goString(ortCall(pGetErrorMessage, status))
	ortCall(pReleaseStatus, status)
	if msg == "" {
		return "（OrtStatus 无消息）"
	}
	return msg
}

// cstr 生成 NUL 结尾的字节切片。
// 调用方必须在使用其指针期间保持切片存活（配合 runtime.KeepAlive）。
func cstr(s string) []byte {
	return append([]byte(s), 0)
}
