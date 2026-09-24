package storage

// ============================================================
// SQLite 驱动注册 —— 纯 Go 实现，无需 cgo / C 编译器
//
// 原先使用 github.com/mattn/go-sqlite3，它是对 SQLite C 源码的封装，
// 必须开启 cgo 并由本机 C 编译器编译，导致缺少编译器时整个后端无法构建。
//
// 改用 github.com/glebarez/go-sqlite（基于 modernc.org/sqlite 的纯 Go 移植）：
// 功能完整（WAL、外键、事务、并发均支持），且默认就会执行
// `PRAGMA busy_timeout(5000)`，与 mattn 驱动的默认行为一致。
//
// 上层统一通过常量 sqliteDriverName 引用驱动名，后续若要更换实现只改这里。
// ============================================================

import (
	// 副作用导入：向 database/sql 注册驱动名 "sqlite"
	_ "github.com/glebarez/go-sqlite"
)

// sqliteDriverName 供 sql.Open 使用的驱动名
const sqliteDriverName = "sqlite"
