# file-filter-provider · 进度

> 创建：2026-06-16
> 模式：adhoc
> skip_test: false · skip_review: false
> tidy: off
> codex MCP: unavailable
> Makefile: present (tests_unit, compile, tests, integration_tests)
> Go: 1.24.0 (via gvm; baseline 失败时为 1.21.13 + macOS 26 ABI 不兼容，已升级)
> Baseline: 通过 (Go 1.24 重跑全绿)
> 干净起点: n/a (tidy off)

## 需求

把 IP / UID 白名单 / 黑名单 filter 从 env var 迁到独立 YAML 文件（独立 ConfigMap），通过 fsnotify 热加载，同时干掉 `service.shouldRateLimitWorker` 里每请求一次 `settings.NewSettings()` 的热路径开销。

### 可观察行为

1. **新增 env var `FILTER_CONFIG_PATH`**（默认空字符串）：
   - 空 → 退回到现有 env var 行为（`WHITELIST_IP_NET` / `BLACKLIST_IP_NET` / `WHITELIST_UID` / `BLACKLIST_UID` 启动时一次性 load，向后兼容老部署）
   - 非空 → 从该 YAML 文件 load filter，env var 被该路径替代；fsnotify watch 该文件所在目录，文件变化自动 reload
2. **配置文件 YAML 格式**：
   ```yaml
   ip:
     allow:
       - 192.168.0.0/24
       - 10.0.0.0/8
     deny:
       - 1.2.3.4/32
   uid:
     allow: ["1001", "1002"]
     deny: ["999"]
   ```
   缺失字段视为空（如只有 `ip` 没 `uid` → uid filter 为空）。
3. **热加载语义**：
   - 文件正常更新（包含 K8s ConfigMap 的 symlink 原子替换模式）→ 解析成功后 atomic-swap，新规则立即对所有请求生效
   - 解析失败（YAML 语法 / CIDR 格式 / UID 非字符串等）→ **保留旧 filter**，记 error log + 增 counter，**不 panic / 不退化**
   - 文件被删除 → 保留旧 filter（防止 ConfigMap 短暂消失导致全部拒绝）
4. **并发安全**：读路径无锁（atomic.Pointer），3w QPS 下不引入额外锁竞争
5. **干掉热路径 NewSettings()**：`shouldRateLimitWorker` 不再调 `settings.NewSettings()`；`ForceFlag` / `OnlyLogOnLimit` / `IPFilter` / `UIDFilter` 改为 NewService 启动时一次性快照 + 注入；行为完全等价
6. **健康检查与启动失败语义**：`FILTER_CONFIG_PATH` 非空但文件首次加载失败 → **启动 panic**（与现有 env var 的 CIDR 格式错时 panic 行为一致）

### MUST

- 现有 `WHITELIST_IP_NET` / `BLACKLIST_IP_NET` / `WHITELIST_UID` / `BLACKLIST_UID` 语义**完全保留**，未设 `FILTER_CONFIG_PATH` 的部署零影响
- 每请求读 filter 必须无锁（atomic.Pointer.Load）
- 解析失败保留旧 filter，绝不让 ratelimit 因为 ConfigMap 短暂错误而下线
- fsnotify watch **目录**而非文件本身（K8s ConfigMap 用 symlink 切换，watch 文件会丢事件）
- 服务关闭时 `Stop()` 干净关闭 fsnotify goroutine

### NEVER

- 不在 hot path 调 `settings.NewSettings()`
- 不在每请求读 filter 时拿锁（`sync.RWMutex.RLock` 在 3w QPS 下也是不可接受的）
- 不要把 fsnotify 的事件直接丢给业务 goroutine（debounce 并在专属 goroutine 处理）
- 不引入新的 yaml 包依赖——复用 `gopkg.in/yaml.v2`（项目已使用）

### 大致文件范围

新增：
- `src/filter/loader.go` — YAML 解析、CIDR 校验、构造 `filter.Filter`
- `src/filter/provider.go` — `FilterProvider` 接口 + 文件实现（atomic.Pointer + fsnotify watcher）+ env var 实现（向后兼容）
- `test/filter/loader_test.go`
- `test/filter/provider_test.go`

修改：
- `src/settings/settings.go` — 加 `FilterConfigPath` env var
- `src/service/ratelimit.go` — `service` 结构持有 `FilterProvider` + 启动期快照 `forceFlag` / `onlyLogOnLimit`；`shouldRateLimitWorker` 移除 `NewSettings()`
- `src/service_cmd/runner/runner.go` — 启动期构造 `FilterProvider`，注入 `NewService`
- `go.mod` — 把 `fsnotify/fsnotify` 从 indirect 提到 direct（已在 go.sum）

## Step 进度

| # | Step ID | 状态 | RED | GREEN | REVIEW | 备注 |
|---|---------|------|-----|-------|--------|------|
| 1 | 01-file-filter-provider | done | 37 断言全红 (sentinel err) | 第 1 轮通过 | review_pass self R02 | R01 revise: P0×0 P1×1 P2×2（runner.Stop 漏调 fp.Stop + watcher.Errors 无日志 + filterPair 注释误导）全部修好；R02 一次过；指标2: skipped (非生产实验性会话，主会话 opt-out，用户如需可手动跑 recording-code-reviews) |

## 整体回归

- final regression: `go test -race -count=1 ./...` 全 14 包 PASS（强制 no-cache）
- make tests_unit: 通过 · make compile: 通过 · make check_format: 通过

## Run 完成

- 模式: Ad-hoc 单 step
- 改动统计:
  - 修改文件 6: `go.mod` / `go.sum` / `src/settings/settings.go` / `src/service/ratelimit.go` / `src/service_cmd/runner/runner.go` / `test/service/ratelimit_test.go`（+89/-25）
  - 新增文件 4: `src/filter/loader.go` / `src/filter/provider.go` / `test/filter/loader_test.go` / `test/filter/provider_test.go`
- 主要交付:
  - 新 env var `FILTER_CONFIG_PATH`（默认空 → 走老 env var 静态加载，向后兼容）
  - `filter.Provider` 接口 + `staticProvider`（env 兜底）/ `fileProvider`（fsnotify watch 父目录 + atomic.Pointer 无锁 swap，K8s ConfigMap symlink 友好）
  - 解析失败/文件删除时保留旧 filter，仅日志 + counter；初次 load 失败 panic 启动（与 env var 错时一致）
  - 干掉 `shouldRateLimitWorker` 每请求一次 `settings.NewSettings()`（envconfig 反射）；`ForceFlag` / `OnlyLogOnLimit` 启动时一次性快照
  - runner.Stop 链式调 `filterProvider.Stop()` 避免 fsnotify watcher + goroutine leak
- TDD 三环节: 🔴 RED 37 断言全红（writer 一次过）→ 🟢 GREEN 一轮通过 → 🔍 REVIEW R01 revise (P1×1 + P2×2) → R02 review_pass
- codex 使用占比: 0%（codex MCP 不可用，reviewer 走 self）
- skip_test/skip_review/tidy: false/false/off
- team 实际形态: ephemeral Agent × 2（writer 1 次 + reviewer 2 次），原因: harness 未注册 TeamCreate/SendMessage（功能差异，不影响产出）

## 部署接入备忘（K8s）

两个独立 ConfigMap：
1. `ratelimit-config` 挂 `/srv/runtime_data/current/config/`（rate-limit descriptor，**已有热加载**走 `RUNTIME_WATCH_ROOT`）
2. `ratelimit-filters` 挂 `/etc/ratelimit/filters.yaml` + 设 `FILTER_CONFIG_PATH=/etc/ratelimit/filters.yaml`

**坑提醒**：不要用 `subPath` 挂 ConfigMap，否则 inotify 收不到事件。直接挂目录即可。

