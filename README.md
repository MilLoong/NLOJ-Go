# NLOJ-Go

NLOJ 的 Go 版本。在线判题后端：用户鉴权、题目管理、提交评测。提交持久化后异步判题，用户代码在 Docker 沙箱里编译运行。

## 模块

| 模块 | 职责 |
|------|------|
| `cmd/nloj` | API 与独立 Worker 的启动入口 |
| `internal/api` | HTTP 路由、追加日志、任务队列与 Worker 通信 |
| `internal/common` | 通用分页 |
| `internal/user` | 用户模型、凭证校验、PBKDF2 |
| `internal/problem` | 题目模型、题型规则、公开样例 |
| `internal/submit` | 提交模型、提交校验、查看权限 |
| `internal/judge` | 容器隔离、输出比对、SPJ、交互与通信 |
| `cmd/fifo` | Linux 容器内的 FIFO 协调程序 |

## 依赖

Go 1.24 或更高版本，项目通过 `go.mod` 管理，HTTP 路由使用 Gin。实际判题需要可用的 Docker Linux 引擎：

```powershell
docker build -t nloj-judge:local -f deploy/judge.Dockerfile .
```

可使用本机 MySQL、Redis 和 RabbitMQ；不需要为中间件启动 Docker。未配置 MySQL 时默认为本地日志 `nloj.journal`；`NLOJ_JOURNAL=-` 使用内存。

| 环境变量 | 作用 |
|------|------|
| `NLOJ_MYSQL_DSN` | MySQL DSN，例如 `user:password@tcp(127.0.0.1:3306)/nloj_go`；数据库须预先创建 |
| `NLOJ_REDIS_URL` | Redis URL，例如 `redis://127.0.0.1:6379/0`；缓存公开题目详情，故障时读 API 持有的状态 |
| `NLOJ_RABBITMQ_URL` | RabbitMQ URL，例如 `amqp://guest:guest@127.0.0.1:5672/`；启用时要求 MySQL |
| `NLOJ_RABBITMQ_QUEUE` | 持久队列名称，默认 `nloj-go.judge`；每组 API / Worker 使用相同值，不与其他实例共用 |

MySQL 模式按用户、题目、用例和提交分别落库：`nloj_go_user`、`nloj_go_problem`、`nloj_go_problem_case`、`nloj_go_submission`。用例关联题目，提交关联用户和题目；提交时保存题目快照，Worker 结果单独去重。题目和提交列表直接用 SQL 分页。

升级旧 Go 版时，先停止旧 API、备份数据库，再启动新版本。首次启动会在事务内迁移旧事件表，保留原表不再写入。仅支持单 API 写入方，不自动导入 C++ 版数据库。

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

配置 RabbitMQ 时，先启动 API 创建 MySQL 表；再用相同 DSN、RabbitMQ URL 和队列名运行 `go run ./cmd/nloj worker`（同时设置 `NLOJ_JUDGE_IMAGE`）。判题结果写入 MySQL 后才确认消息。此模式每个队列使用一个消费者。

未配置 RabbitMQ 时，可使用 HTTP Worker，API 和 Worker 需使用相同的随机 `NLOJ_WORKER_TOKEN`（至少 32 字节）。

- Worker：设置 `NLOJ_JUDGE_IMAGE` 后执行 `go run ./cmd/nloj worker`，监听 `127.0.0.1:8081`
- API：设置 `NLOJ_WORKER_URL=http://127.0.0.1:8081/judge` 后执行 `go run ./cmd/nloj`

## 语言与题型

- 语言：`CPP` / `C` / `PYTHON` / `JAVA` / `GO`
- 题型：`STANDARD` / `INTERACTIVE` / `COMMUNICATION`
- 比对：`EXACT` / `SPJ`（普通题）

交互题和通信题仅接受 C / C++。通信题用 `===NLOJ_FILE:alice===` 和 `===NLOJ_FILE:bob===` 分隔两份源码。题目用例通过 `samples` 字段提交，仅标记为 `sample: true` 的条目会出现在公开详情中；更新时省略该字段则保留原用例。

判题容器禁用网络。当前计时包含容器启动开销，不提供峰值内存。

## 与 C++ 版对比

| 项目 | C++ 版 | Go 版 |
|------|--------|-------|
| 题目详情吞吐量 | 184.21–184.86 req/s | 2,768.74–2,908.78 req/s |
| 题目详情 P50 | 44.02–44.04 ms | 2.73–2.86 ms |
| 题目详情 P95 | 48.04–48.07 ms | 3.73–3.83 ms |
| 题目详情 P99 | 48.15–48.32 ms | 4.32–4.43 ms |
| 压测请求失败数 | 0 / 2,788 | 0 / 42,580 |

同机、同题、同一 MySQL 和 Redis；预热后以 8 并发各跑 3 轮，每轮 5 秒。只比较题目详情接口，不代表语言本身的性能差距。

## License

MIT
