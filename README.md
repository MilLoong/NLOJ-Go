# NLOJ-Go

NLOJ 的 Go 版本。在线判题后端：用户鉴权、题目管理、提交评测。提交持久化后异步判题，用户代码在 Docker 沙箱里编译运行。

## 模块

| 模块 | 职责 |
|------|------|
| `cmd/nloj` | API 与独立 Worker 的启动入口 |
| `internal/app` | 用户、题目、提交 API，追加日志、队列和 Worker 通信 |
| `internal/judge` | 容器隔离、输出比对、SPJ、交互与通信 |
| `cmd/fifo` | Linux 容器内的 FIFO 协调程序 |

## 依赖

Go 1.24 或更高版本，项目通过 `go.mod` 管理。实际判题需要可用的 Docker Linux 引擎：

```powershell
docker build -t nloj-judge:local -f deploy/judge.Dockerfile .
```

Go 版默认使用本地日志 `nloj.journal`，无需启动 MySQL、Redis 或 RabbitMQ。`NLOJ_JOURNAL` 可指定文件路径，设为 `-` 时只使用内存。同一个日志文件只供一个 API 进程使用。

## 构建

```powershell
go build ./...
go test ./...
```

### 启动 API

```powershell
$env:NLOJ_ADMIN_PASSWORD = 'replace-with-local-admin-password'
$env:NLOJ_JUDGE_IMAGE = 'nloj-judge:local'
go run ./cmd/nloj
```

服务监听 `127.0.0.1:8080`，接口前缀为 `/api/v1`。不配置管理员密码时不创建管理员；不配置判题镜像或独立 Worker 时，提交保留为 `PENDING`。

### 独立判题进程

API 和 Worker 使用相同的随机 `NLOJ_WORKER_TOKEN`（至少 32 字节）。

- Worker：设置 `NLOJ_JUDGE_IMAGE` 后执行 `go run ./cmd/nloj worker`，监听 `127.0.0.1:8081`
- API：设置 `NLOJ_WORKER_URL=http://127.0.0.1:8081/judge` 后执行 `go run ./cmd/nloj`

## 语言与题型

- 语言：`CPP` / `C` / `PYTHON` / `JAVA` / `GO`
- 题型：`STANDARD` / `INTERACTIVE` / `COMMUNICATION`
- 比对：`EXACT` / `SPJ`（普通题）

交互题和通信题仅接受 C / C++。通信题用 `===NLOJ_FILE:alice===` 和 `===NLOJ_FILE:bob===` 分隔两份源码。题目用例通过 `samples` 字段提交，仅标记为 `sample: true` 的条目会出现在公开详情中；更新时省略该字段则保留原用例。

判题容器禁用网络。当前计时包含容器启动开销，不提供峰值内存。

## License

MIT
