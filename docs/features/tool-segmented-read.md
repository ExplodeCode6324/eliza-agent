# Tool 结果分段读取

> **文件**: `tools.go` (ReadFileTool)  
> **版本**: v0.2.0

---

## 设计目标

大文件读取不再硬截断，LLM 可通过 offset/limit 参数分段读取。

当前实现使用 os.Open + Seek + 有界 buffer，不会先把整个文件读入内存。单次读取还会受到 .env 最大读取字节数和系统内存 25% 硬上限保护。

## 参数

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `path` | string | (必填) | 文件路径 |
| `offset` | integer | 0 | 起始字节偏移量 |
| `limit` | integer | 10000 | 最大返回字节数 |

## 行为

```
read_file(path="/large.log")
  → 返回前 10000 字节
  → 末尾: [TRUNCATED total=50000 offset=0 returned=10000 remaining=40000]

read_file(path="/large.log", offset=10000, limit=10000)
  → 返回 10000-19999 字节
  → 末尾: [TRUNCATED total=50000 offset=10000 returned=10000 remaining=30000]

read_file(path="/large.log", offset=40000, limit=10000)
  → 返回 40000-49999 字节 (最后一段)
  → 末尾: [END total=50000 offset=40000]

read_file(path="/large.log", offset=50000)
  → [EOF] offset=50000 超出文件大小 50000 字节
```

## TRUNCATED 标记格式

```
[TRUNCATED total=<总字节> offset=<起始> returned=<已返回> remaining=<剩余>]
```

## LLM 交互流程

```
LLM 调用 read_file → 收到 [TRUNCATED] 标记
→ 判断是否需要继续读取
→ 调用 read_file(offset=<上次结束位置>) 读下一段
```

## 边界处理

- `offset < 0` → 自动修正为 0
- `offset >= totalSize` → 返回 `[EOF]` 提示
- `offset + limit > totalSize` → 返回至文件末尾 + `[END]` 标记
- 文件读取错误 → 返回 error（不截断）
